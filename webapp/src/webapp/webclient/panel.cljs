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
   ["@codemirror/state" :as cm-state]
   ["@codemirror/view" :as cm-view]
   ["@heroicons/react/20/solid" :as hero-solid-icon]
   ["@radix-ui/themes" :refer [Box Flex Spinner Text Tooltip]]
   ["@uiw/codemirror-theme-material" :refer [materialDark materialLight]]
   ["@uiw/react-codemirror" :as CodeMirror]
   ["allotment" :refer [Allotment]]
   ["codemirror-copilot" :refer [clearLocalCache]]
   ["codemirror-lang-elixir" :as cm-elixir]
   ["lucide-react" :refer [Info]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.keyboard-shortcuts :as keyboard-shortcuts]
   [webapp.features.promotion :as promotion]
   [webapp.formatters :as formatters]
   [webapp.parallel-mode.components.execution-summary.main :as execution-summary]
   [webapp.parallel-mode.components.modal.main :as parallel-mode-modal]
   [webapp.webclient.components.connection-dialog :as connection-dialog]
   [webapp.webclient.components.header :as header]
   [webapp.webclient.components.language-select :as language-select]
   [webapp.webclient.components.panels.database-schema :as database-schema-panel]
   [webapp.webclient.components.panels.metadata :as metadata-panel]
   [webapp.webclient.components.side-panel :refer [with-panel]]
   [webapp.webclient.log-area.main :as log-area]
   [webapp.webclient.quickstart :as quickstart]))

(defn discover-connection-type [connection]
  (cond
    (not (cs/blank? (:subtype connection))) (:subtype connection)
    (not (cs/blank? (:icon_name connection))) (:icon_name connection)
    :else (:type connection)))

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

(defn create-codemirror-extensions [parser
                                    keymap
                                    is-template-ready?
                                    clipboard-disabled?]

  (let [clipboard-keymap (when clipboard-disabled?
                           [{:key "Mod-c"
                             :run (fn [_]
                                    (rf/dispatch [:show-snackbar {:level :error
                                                                  :text "Clipboard copy/cut operations are disabled by administrator"}])
                                    true)}
                            {:key "Mod-x"
                             :run (fn [_]
                                    (rf/dispatch [:show-snackbar {:level :error
                                                                  :text "Clipboard copy/cut operations are disabled by administrator"}])
                                    true)}])
        extensions
        (concat
         [(.of cm-view/keymap (clj->js keymap))]
         (when clipboard-disabled?
           [(.of cm-view/keymap (clj->js clipboard-keymap))])
         parser
         (when is-template-ready?
           [(.of (.-editable cm-view/EditorView) false)
            (.of (.-readOnly cm-state/EditorState) true)]))]

    extensions))

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
  (let [gateway-info (rf/subscribe [:gateway->info])
        clipboard-disabled? (rf/subscribe [:gateway->clipboard-disabled?])
        db-connections (rf/subscribe [:connections])
        primary-connection (rf/subscribe [:primary-connection/selected])
        active-panel (rf/subscribe [:webclient->active-panel])
        parallel-mode-active? (rf/subscribe [:parallel-mode/is-active?])
        parallel-mode-promotion-seen (rf/subscribe [:parallel-mode/promotion-seen])

        dark-mode? (r/atom (= (.getItem js/localStorage "dark-mode") "true"))
        db-schema-collapsed? (r/atom false)
        horizontal-pane-sizes (mapv js/parseInt
                                    (cs/split
                                     (or (.getItem js/localStorage "editor-horizontal-pane-sizes") "650,210") ","))
        script (r/atom (get-code-from-localstorage))
        metadata (r/atom [])
        metadata-key (r/atom "")
        metadata-value (r/atom "")]
    (rf/dispatch [:gateway->get-info])

    (fn [{:keys [script-output]}]
      (let [current-connection @primary-connection
            connection-type (discover-connection-type current-connection)
            disabled-download (-> @gateway-info :data :disable_sessions_download)
            exec-enabled? (= "enabled" (:access_mode_exec current-connection))
            no-connection-selected? (not @primary-connection)
            run-disabled? (or (not exec-enabled?) no-connection-selected?)
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
                             language-parser-case
                             keymap
                             false
                             @clipboard-disabled?)

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

            panel-content (fn [active-panel]
                            (case active-panel
                              :metadata {:title "Metadata"
                                         :content [metadata-panel/main {:metadata metadata
                                                                        :metadata-key metadata-key
                                                                        :metadata-value metadata-value}]}
                              nil))]

        (cond
          (and (empty? (:results @db-connections))
               (not (:loading @db-connections)))
          [quickstart/main]

          (not @parallel-mode-promotion-seen)
          [:> Box {:class "bg-gray-1 h-full"}
           [promotion/parallel-mode-promotion {:mode :empty-state}]]

          :else
          [:<>
           [:> Box {:class (str "h-full bg-gray-2 overflow-hidden "
                                (when @dark-mode?
                                  "dark"))}
            [connection-dialog/connection-dialog]
            [parallel-mode-modal/parallel-mode-modal]
            [execution-summary/execution-summary-modal]

            [header/main
             dark-mode?
             #(rf/dispatch [:editor-plugin/submit-task {:script @script}])]

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
                               :vertical true
                               :separator false}
                 [:div {:class "relative w-full h-full"}
                  [:div {:class "h-full flex flex-col"}
                   (when (= "custom" (:type current-connection))
                     [connection-state-indicator @dark-mode? (:command current-connection)])
                   [codemirror-editor
                    {:value @script
                     :theme (if @dark-mode?
                              materialDark
                              materialLight)
                     :extensions codemirror-exts
                     :on-change optimized-change-handler}]]]

                 [:> Flex {:direction "column" :justify "between" :class "h-full border-t border-gray-3"}
                  [log-area/main
                   connection-type
                   @parallel-mode-active?
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

             (panel-content @active-panel)]]])))))

(def main
  (r/create-class
   {:reagent-render
    (fn []
      (let [script-response (rf/subscribe [:editor-plugin->script])]
        (rf/dispatch [:editor-plugin->clear-script])
        (rf/dispatch [:audit->clear-session])
        (rf/dispatch [:plugins->get-my-plugins])
        (rf/dispatch [:jira-templates->get-all])
        (rf/dispatch [:jira-integration->get])
        (rf/dispatch [:search/clear-term])

        (fn []
          (clearLocalCache)
          [editor {:script-output script-response}])))}))
