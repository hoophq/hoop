(ns webapp.webclient.panel
  (:require
   ["@codemirror/commands" :as cm-commands]
   ["@codemirror/lang-sql" :refer [MSSQL MySQL PLSQL PostgreSQL sql]]
   ["@codemirror/language" :as cm-language]
   ["@codemirror/legacy-modes/mode/clojure" :as cm-clojure]
   ["@codemirror/legacy-modes/mode/javascript" :as cm-javascript]
   ["@codemirror/legacy-modes/mode/python" :as cm-python]
   ["@codemirror/legacy-modes/mode/ruby" :as cm-ruby]
   ["@codemirror/legacy-modes/mode/shell" :as cm-shell]
   ["codemirror-lang-elixir" :as cm-elixir]
   ["@codemirror/state" :as cm-state]
   ["@codemirror/view" :as cm-view]
   ["@heroicons/react/20/solid" :as hero-solid-icon]
   ["@radix-ui/themes" :refer [Box Flex Spinner Tooltip Text]]
   ["@uiw/codemirror-theme-material" :refer [materialDark materialLight]]
   ["@uiw/react-codemirror" :as CodeMirror]
   ["allotment" :refer [Allotment]]
   ["codemirror-copilot" :refer [clearLocalCache inlineCopilot]]
   ["lucide-react" :refer [Info]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.formatters :as formatters]
   [webapp.components.keyboard-shortcuts :as keyboard-shortcuts]
   [webapp.webclient.codemirror.extensions :as extensions]
   [webapp.webclient.components.primary-connection-list :as primary-connection-list]
   [webapp.webclient.components.connection-dialog :as connection-dialog]
   [webapp.webclient.components.header :as header]
   [webapp.webclient.components.language-select :as language-select]
   [webapp.webclient.components.panels.multiple-connections :as multiple-connections-panel]
   [webapp.webclient.components.panels.metadata :as metadata-panel]
   [webapp.webclient.components.panels.database-schema :as database-schema-panel]
   [webapp.webclient.components.side-panel :refer [with-panel]]
   [webapp.webclient.exec-multiples-connections.exec-list :as multiple-connections-exec-list-component]
   [webapp.webclient.log-area.main :as log-area]
   [webapp.webclient.quickstart :as quickstart]))

(defn discover-connection-type [connection]
  (cond
    (not (cs/blank? (:subtype connection))) (:subtype connection)
    (not (cs/blank? (:icon_name connection))) (:icon_name connection)
    :else (:type connection)))

(defn metadata->json-stringify
  [metadata]
  (->> metadata
       (filter (fn [{:keys [key value]}]
                 (not (or (cs/blank? key) (cs/blank? value)))))
       (map (fn [{:keys [key value]}] {key value}))
       (reduce into {})
       (clj->js)))

(defn- get-code-from-localstorage []
  (let [item (.getItem js/localStorage :code-tmp-db)
        object (js->clj (.parse js/JSON item))]
    (or (get object "code") "")))

(def ^:private code-saved-status (r/atom :saved)) ; :edited | :saved
(def ^:private is-typing (r/atom false))
(def ^:private typing-timer (r/atom nil))

(defn- save-code-to-localstorage [code-string]
  (let [code-tmp-db {:date (.now js/Date)
                     :code code-string}
        code-tmp-db-json (.stringify js/JSON (clj->js code-tmp-db))]
    (.setItem js/localStorage :code-tmp-db code-tmp-db-json)
    (reset! code-saved-status :saved)))


(defmulti ^:private saved-status-el identity)
(defmethod ^:private saved-status-el :saved [_]
  [:div {:class "flex flex-row-reverse text-gray-11"}
   [:div {:class "flex items-center gap-small"}
    [:> hero-solid-icon/CheckIcon {:class "h-4 w-4 shrink-0 text-green-500"
                                   :aria-hidden "true"}]
    [:span {:class "text-xs"}
     "Saved!"]]])
(defmethod ^:private saved-status-el :edited [_]
  [:div {:class "flex flex-row-reverse text-gray-11"}
   [:div {:class "flex items-center gap-small"}
    [:> Spinner {:size "1" :color "gray"}]
    [:span {:class "text-xs italic"}
     "Edited"]]])

(def current-sql-parser (r/atom nil))

(defn should-recreate-parser? [prev-lang current-lang]
  (or (nil? prev-lang)
      (not= prev-lang current-lang)))

(defn get-or-create-sql-parser [current-language]
  (let [prev-parser-info (:info @current-sql-parser)
        prev-lang (:language prev-parser-info)]

    (if (should-recreate-parser? prev-lang current-language)
      (let [basic-parser (case current-language
                           "postgres" [(sql (.assign js/Object (.-dialect PostgreSQL) #js{}))]
                           "mysql" [(sql (.assign js/Object (.-dialect MySQL) #js{}))]
                           "mssql" [(sql (.assign js/Object (.-dialect MSSQL) #js{}))]
                           "oracledb" [(sql (.assign js/Object (.-dialect PLSQL) #js{}))]
                           "command-line" [(.define cm-language/StreamLanguage cm-shell/shell)]
                           "javascript" [(.define cm-language/StreamLanguage cm-javascript/javascript)]
                           "nodejs" [(.define cm-language/StreamLanguage cm-javascript/javascript)]
                           "mongodb" [(.define cm-language/StreamLanguage cm-javascript/javascript)]
                           "ruby-on-rails" [(.define cm-language/StreamLanguage cm-ruby/ruby)]
                           "python" [(.define cm-language/StreamLanguage cm-python/python)]
                           "clojure" [(.define cm-language/StreamLanguage cm-clojure/clojure)]
                           "elixir" [(cm-elixir/elixir)]
                           "" [(.define cm-language/StreamLanguage cm-shell/shell)]
                           [(.define cm-language/StreamLanguage cm-shell/shell)])]

        ;; Use basic parser
        (reset! current-sql-parser {:parser basic-parser
                                    :info {:language current-language}})

        ;; Return basic parser
        basic-parser)

      (:parser @current-sql-parser))))

(def editor-debounce-time 750)

;; Optimization of the typing state update function
(defn update-global-typing-state [is-typing?]
  (when (not= @is-typing is-typing?)
    (reset! is-typing is-typing?)
    (aset js/window "is_typing" is-typing?)))

(def codemirror-extensions-cache (r/atom {}))

(defn create-codemirror-extensions [current-language
                                    parser
                                    keymap
                                    feature-ai-ask
                                    is-one-connection-selected?
                                    connection-subtype
                                    is-template-ready?]

  (let [cache-key [current-language
                   (hash parser)
                   feature-ai-ask
                   is-one-connection-selected?
                   is-template-ready?]]

    (or (get @codemirror-extensions-cache cache-key)
        (let [extensions
              (concat
               (when (and (= feature-ai-ask "enabled")
                          is-one-connection-selected?)
                 [(inlineCopilot
                   #js{:getSuggestions (fn [prefix suffix]
                                         (extensions/fetch-autocomplete
                                          connection-subtype
                                          prefix
                                          suffix))
                       :debounceMs 1200
                       :maxPrefixLength 500
                       :maxSuffixLength 500})])
               [(.of cm-view/keymap (clj->js keymap))]
               parser
               (when is-template-ready?
                 [(.of (.-editable cm-view/EditorView) false)
                  (.of (.-readOnly cm-state/EditorState) true)]))]

          (swap! codemirror-extensions-cache assoc cache-key extensions)
          extensions))))

(def codemirror-editor
  (r/create-class
   {:display-name "OptimizedCodeMirror"

    :should-component-update
    (fn [_ [_ old-props] [_ new-props]]
      (let [should-update (or
                           (not= (:value old-props) (:value new-props))
                           (not= (:theme old-props) (:theme new-props))
                           (not= (hash (:extensions old-props)) (hash (:extensions new-props))))]
        should-update))

    :reagent-render
    (fn [{:keys [value theme extensions on-change]}]
      [:> CodeMirror/default
       {:value value
        :height "100%"
        :className "h-full text-sm"
        :theme theme
        :basicSetup #js{:defaultKeymap false}
        :onChange on-change
        :extensions (clj->js extensions)}])}))

(defn connection-state-indicator [dark-mode? command]
  [:> Box {:class (str "p-3 " (if dark-mode? "bg-[#2e3235]" "bg-[#FAFAFA]"))}

   [:> Flex {:align "center" :gap "1"}
    [:> Box {:class "px-2 rounded-md bg-[--gray-10]"}
     [:> Text {:as "span" :size "1" :weight "medium" :class "text-gray-1"} "stdin → "]
     [:> Text {:as "span" :size "1" :weight "medium" :class "text-gray-1"} (cs/join " " command)]]
    [:> Tooltip {:content (str "Your script streams to " (cs/join " " command) " via stdin")}
     [:> Info {:class "shrink-0 text-gray-11"}]]]])



(defn editor []
  (let [user (rf/subscribe [:users->current-user])
        gateway-info (rf/subscribe [:gateway->info])
        db-connections (rf/subscribe [:connections])
        multi-selected-connections (rf/subscribe [:multiple-connections/selected])
        multi-exec (rf/subscribe [:multiple-connection-execution/modal])
        primary-connection (rf/subscribe [:primary-connection/selected])
        use-compact-ui? (rf/subscribe [:webclient/use-compact-ui?])

        active-panel (r/atom nil)
        multi-run-panel? (r/atom false)
        dark-mode? (r/atom (= (.getItem js/localStorage "dark-mode") "true"))
        db-schema-collapsed? (r/atom false)

        handle-connection-modes! (fn [current-connection]
                                   (when current-connection
                                     (let [exec-enabled? (= "enabled" (:access_mode_exec current-connection))]
                                       (when (not exec-enabled?)
                                         (reset! active-panel nil)))))

        vertical-pane-sizes (mapv js/parseInt
                                  (cs/split
                                   (or (.getItem js/localStorage "editor-vertical-pane-sizes") "270,950") ","))
        horizontal-pane-sizes (mapv js/parseInt
                                    (cs/split
                                     (or (.getItem js/localStorage "editor-horizontal-pane-sizes") "650,210") ","))
        script (r/atom (get-code-from-localstorage))
        metadata (r/atom [])
        metadata-key (r/atom "")
        metadata-value (r/atom "")]
    (rf/dispatch [:gateway->get-info])

    (fn [{:keys [script-output]}]
      (handle-connection-modes! @primary-connection)

      (let [is-one-connection-selected? @(rf/subscribe [:execution/is-single-mode])
            feature-ai-ask (or (get-in @user [:data :feature_ask_ai]) "disabled")
            current-connection @primary-connection
            connection-type (discover-connection-type current-connection)
            disabled-download (-> @gateway-info :data :disable_sessions_download)
            exec-enabled? (= "enabled" (:access_mode_exec current-connection))
            no-connection-selected? (and (empty? @multi-selected-connections)
                                         (not @primary-connection))
            run-disabled? (or (not exec-enabled?) no-connection-selected?)
            reset-metadata (fn []
                             (reset! metadata [])
                             (reset! metadata-key "")
                             (reset! metadata-value ""))
            keymap [{:key "Mod-Enter"
                     :run (fn [^cm-state/StateCommand config]
                            (when-not run-disabled?
                              (let [state (.-state config)
                                    doc (.-doc state)]
                                (rf/dispatch [:editor-plugin/submit-task
                                              {:script (.sliceString ^cm-state/Text doc 0 (.-length doc))}]))))
                     :preventDefault true}
                    {:key "Mod-Shift-Enter"
                     :run (fn [^cm-state/StateCommand config]
                            (when-not run-disabled?
                              (let [ranges (.-ranges (.-selection (.-state config)))
                                    from (.-from (first ranges))
                                    to (.-to (first ranges))]
                                (rf/dispatch [:editor-plugin/submit-task
                                              {:script
                                               (.sliceString ^cm-state/Text (.-doc (.-state config)) from to)}]))))
                     :preventDefault true}
                    {:key "Alt-ArrowLeft"
                     :mac "Ctrl-ArrowLeft"
                     :run cm-commands/cursorSyntaxLeft
                     :shift cm-commands/selectSyntaxLeft}
                    {:key "Alt-ArrowRight"
                     :mac "Ctrl-ArrowRight"
                     :run cm-commands/cursorSyntaxRight
                     :shift cm-commands/selectSyntaxRight}
                    {:key "Alt-ArrowUp" :run cm-commands/moveLineUp}
                    {:key "Shift-Alt-ArrowUp" :run cm-commands/copyLineUp}
                    {:key "Alt-ArrowDown" :run cm-commands/moveLineDown}
                    {:key "Shift-Alt-ArrowDown" :run cm-commands/copyLineDown}
                    {:key "Escape" :run cm-commands/simplifySelection}
                    {:key "Enter" :run cm-commands/insertNewlineAndIndent}
                    {:key "Alt-l" :mac "Ctrl-l" :run cm-commands/selectLine}
                    {:key "Mod-i" :run cm-commands/selectParentSyntax :preventDefault true}
                    {:key "Mod-[" :run cm-commands/indentLess :preventDefault true}
                    {:key "Mod-]" :run cm-commands/indentMore :preventDefault true}
                    {:key "Mod-Alt-\\" :run cm-commands/indentSelection}
                    {:key "Shift-Mod-k" :run cm-commands/deleteLine}
                    {:key "Shift-Mod-\\" :run cm-commands/cursorMatchingBracket}
                    {:key "Mod-/" :run cm-commands/toggleComment}
                    {:key "Alt-A" :run cm-commands/toggleBlockComment}]
            language-info @(rf/subscribe [:editor-plugin/language])
            current-language (or (:selected language-info) (:default language-info))
            language-parser-case (get-or-create-sql-parser current-language)

            codemirror-exts (create-codemirror-extensions
                             current-language
                             language-parser-case
                             keymap
                             feature-ai-ask
                             is-one-connection-selected?
                             (:subtype current-connection)
                             false)

            optimized-change-handler (fn [value _]
                                       (reset! script value)
                                       (reset! code-saved-status :edited)
                                       (update-global-typing-state true)
                                       (when @typing-timer (js/clearTimeout @typing-timer))
                                       (reset! typing-timer
                                               (js/setTimeout
                                                (fn []
                                                  (update-global-typing-state false)
                                                  (save-code-to-localstorage value))
                                                editor-debounce-time)))


            panel-content (case @active-panel
                            :metadata (metadata-panel/main {:metadata metadata
                                                            :metadata-key metadata-key
                                                            :metadata-value metadata-value})
                            nil)]

        (if (and (empty? (:results @db-connections))
                 (not (:loading @db-connections)))
          [quickstart/main]

          [:<>
           [:> Box {:class (str "h-full bg-gray-2 overflow-hidden "
                                (when @dark-mode?
                                  "dark"))}
            (when @use-compact-ui?
              [connection-dialog/connection-dialog])

            [header/main
             active-panel
             multi-run-panel?
             dark-mode?
             #(rf/dispatch [:editor-plugin/submit-task {:script @script}])]

            (if @use-compact-ui?
              ;; Compact layout
              [with-panel
               (boolean @active-panel)
               [:> Box {:class "flex h-terminal-content overflow-hidden"}
                [:> Allotment {:key (str "compact-allotment-" @db-schema-collapsed?)
                               :separator false}

                 (when (and current-connection
                            (or (= "database" (:type current-connection))
                                (= "dynamodb" (:subtype current-connection))
                                (= "cloudwatch" (:subtype current-connection))))
                   [:> (.-Pane Allotment) {:minSize (if @db-schema-collapsed? 64 250)
                                           :maxSize (if @db-schema-collapsed? 64 400)}
                    [database-schema-panel/main {:connection current-connection
                                                 :collapsed? @db-schema-collapsed?
                                                 :on-toggle-collapse #(swap! db-schema-collapsed? not)}]])

                 [:> (.-Pane Allotment)
                  [:> Allotment {:defaultSizes horizontal-pane-sizes
                                 :onDragEnd #(.setItem js/localStorage "editor-horizontal-pane-sizes" (str %))
                                 :vertical true}
                   [:div {:class "relative w-full h-full"}
                    [:div {:class "h-full flex flex-col"}
                     (when (and (empty? @multi-selected-connections)
                                (= "custom" (:type current-connection)))
                       [connection-state-indicator @dark-mode? (:command current-connection)])
                     [codemirror-editor
                      {:value @script
                       :theme (if @dark-mode?
                                materialDark
                                materialLight)
                       :extensions codemirror-exts
                       :on-change optimized-change-handler}]]]

                   [:> Flex {:direction "column" :justify "between" :class "h-full"}
                    [log-area/main
                     connection-type
                     is-one-connection-selected?
                     @dark-mode?
                     (not disabled-download)]

                    [:div {:class "bg-gray-1"}
                     [:footer {:class "flex justify-between items-center p-2 gap-small"}
                      [:div {:class "flex items-center gap-small"}
                       [saved-status-el @code-saved-status]
                       (when (:execution_time (:data @script-output))
                         [:div {:class "flex items-center gap-small"}
                          [:> hero-solid-icon/ClockIcon {:class "h-4 w-4 shrink-0 text-white"
                                                         :aria-hidden "true"}]
                          [:span {:class "text-xs text-gray-11"}
                           (str "Last execution time " (formatters/time-elapsed (:execution_time (:data @script-output))))]])]
                      [:div {:class "flex-end items-center gap-regular pr-4 flex"}
                       [:div {:class "mr-3"}
                        [keyboard-shortcuts/keyboard-shortcuts-button]]
                       [language-select/main current-connection]]]]]]]]]
               panel-content]

              ;; Classic layout
              [with-panel
               (boolean @active-panel)
               [:> Box {:class "flex h-terminal-content overflow-hidden"}
                [:> Allotment {:defaultSizes vertical-pane-sizes
                               :onDragEnd #(.setItem js/localStorage "editor-vertical-pane-sizes" (str %))}
                 [:> (.-Pane Allotment) {:minSize 270}
                  [:aside {:class "h-full flex flex-col gap-8 border-r-2 border-[--gray-3]"}
                   (if @multi-run-panel?
                     [multiple-connections-panel/main dark-mode? false]
                     [primary-connection-list/main dark-mode?])]]

                 [:> Allotment {:defaultSizes horizontal-pane-sizes
                                :onDragEnd #(.setItem js/localStorage "editor-horizontal-pane-sizes" (str %))
                                :vertical true}
                  [:div {:class "relative w-full h-full"}
                   [:div {:class "h-full flex flex-col"}
                    (when (and
                           (empty? @multi-selected-connections)
                           (= "custom" (:type current-connection)))
                      [connection-state-indicator @dark-mode? (:command current-connection)])
                    [codemirror-editor
                     {:value @script
                      :theme (if @dark-mode?
                               materialDark
                               materialLight)
                      :extensions codemirror-exts
                      :on-change optimized-change-handler}]]]

                  [:> Flex {:direction "column" :justify "between" :class "h-full"}
                   [log-area/main
                    connection-type
                    is-one-connection-selected?
                    @dark-mode?
                    (not disabled-download)]

                   [:div {:class "bg-gray-1"}
                    [:footer {:class "flex justify-between items-center p-2 gap-small"}
                     [:div {:class "flex items-center gap-small"}
                      [saved-status-el @code-saved-status]
                      (when (:execution_time (:data @script-output))
                        [:div {:class "flex items-center gap-small"}
                         [:> hero-solid-icon/ClockIcon {:class "h-4 w-4 shrink-0 text-white"
                                                        :aria-hidden "true"}]
                         [:span {:class "text-xs text-gray-11"}
                          (str "Last execution time " (formatters/time-elapsed (:execution_time (:data @script-output))))]])]
                     [:div {:class "flex-end items-center gap-regular pr-4 flex"}
                      [:div {:class "mr-3"}
                       [keyboard-shortcuts/keyboard-shortcuts-button]]
                      [language-select/main current-connection]]]]]]]]
               panel-content])]

           (when (seq (:data @multi-exec))
             [multiple-connections-exec-list-component/main
              (map #(into {} {:connection-name (:name %)
                              :script @script
                              :metadata (metadata->json-stringify
                                         (conj @metadata {:key @metadata-key :value @metadata-value}))
                              :type (:type %)
                              :subtype (:subtype %)
                              :session-id nil
                              :status :ready})
                   @multi-selected-connections)
              reset-metadata])])))))

(def main
  (r/create-class
   {:component-will-unmount
    (fn [this]
      (js/window.Intercom "update" #js{:hide_default_launcher false}))

    :reagent-render
    (fn []
      (let [script-response (rf/subscribe [:editor-plugin->script])
            use-compact-ui? (rf/subscribe [:webclient/use-compact-ui?])]
        (rf/dispatch [:editor-plugin->clear-script])
        (rf/dispatch [:editor-plugin->clear-connection-script])
        (rf/dispatch [:audit->clear-session])
        (rf/dispatch [:plugins->get-my-plugins])
        (rf/dispatch [:jira-templates->get-all])
        (rf/dispatch [:jira-integration->get])
        (rf/dispatch [:search/clear-term])
        (rf/dispatch [:editor-plugin->get-run-connection-list])

        (when-not @use-compact-ui?
          (rf/dispatch [:connections->get-connections]))

        (js/window.Intercom "update" #js{:hide_default_launcher true})
        (fn []
          (clearLocalCache)
          [editor {:script-output script-response}])))}))
