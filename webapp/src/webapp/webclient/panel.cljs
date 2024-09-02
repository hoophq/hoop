(ns webapp.webclient.panel
  (:require ["@codemirror/commands" :as cm-commands]
            ["@codemirror/lang-sql" :refer [MSSQL MySQL PostgreSQL PLSQL sql]]
            ["@codemirror/language" :as cm-language]
            ["@codemirror/legacy-modes/mode/clojure" :as cm-clojure]
            ["@codemirror/legacy-modes/mode/javascript" :as cm-javascript]
            ["@codemirror/legacy-modes/mode/python" :as cm-python]
            ["@codemirror/legacy-modes/mode/ruby" :as cm-ruby]
            ["@codemirror/legacy-modes/mode/shell" :as cm-shell]
            ["@codemirror/state" :as cm-state]
            ["@codemirror/view" :as cm-view]
            ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["@uiw/codemirror-theme-dracula" :refer [dracula]]
            ["@uiw/codemirror-theme-github" :refer [githubDark]]
            ["@uiw/codemirror-theme-nord" :refer [nord]]
            ["@uiw/codemirror-theme-sublime" :refer [sublime]]
            ["@uiw/react-codemirror" :as CodeMirror]
            ["allotment" :refer [Allotment]]
            ["codemirror-copilot" :refer [clearLocalCache inlineCopilot]]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.forms :as forms]
            [webapp.webclient.aside.main :as aside]
            [webapp.webclient.codemirror.extensions :as extensions]
            [webapp.webclient.log-area.main :as log-area]
            [webapp.webclient.quickstart :as quickstart]
            [webapp.webclient.exec-multiples-connections.exec-list :as multiple-connections-exec-list-component]
            [webapp.webclient.runbooks.form :as runbooks-form]
            [webapp.formatters :as formatters]
            [webapp.subs :as subs]))

(defn discorver-connection-type [connection]
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

(def ^:private timer (r/atom nil))
(def ^:private code-saved-status (r/atom :saved)) ; :edited | :saved

(defn- save-code-to-localstorage [code-string]
  (let [code-tmp-db {:date (.now js/Date)
                     :code code-string}
        code-tmp-db-json (.stringify js/JSON (clj->js code-tmp-db))]
    (.setItem js/localStorage :code-tmp-db code-tmp-db-json)
    (reset! code-saved-status :saved)))

(defn- auto-save [^cm-view/ViewUpdate view-update script]
  (when (.-docChanged view-update)
    (reset! code-saved-status :edited)
    (let [code-string (.toString (.-doc (.-state (.-view view-update))))]
      (when @timer (js/clearTimeout @timer))
      (reset! timer (js/setTimeout #(save-code-to-localstorage code-string) 1000))
      (reset! script code-string))))

(defn- submit-task [e script selected-connections atom-exec-list-open? metadata script-response]
  (println @script-response)
  (let [connection-type (discorver-connection-type (first selected-connections))
        change-to-tabular? (and (some (partial = connection-type) ["mysql" "postgres" "sql-server" "oracledb" "mssql" "database"])
                                (< (count @script-response) 1))]
    (when (.-preventDefault e) (.preventDefault e))

    (if (and (seq selected-connections)
             (> (count selected-connections) 1))
      (reset! atom-exec-list-open? true)

      (if (first selected-connections)
        (do
          (when change-to-tabular?
            (reset! log-area/selected-tab "Tabular"))
          (rf/dispatch [:editor-plugin->exec-script {:script script
                                                     :connection-name (:name (first selected-connections))
                                                     :metadata (metadata->json-stringify metadata)}]))

        (rf/dispatch [:show-snackbar {:level :info
                                      :text "You must choose a connection"}])))))

(defmulti ^:private saved-status-el identity)
(defmethod ^:private saved-status-el :saved [_]
  [:div {:class "flex flex-row-reverse"}
   [:div {:class "flex items-center gap-small"}
    [:> hero-solid-icon/CheckIcon {:class "h-4 w-4 shrink-0 text-green-500"
                                   :aria-hidden "true"}]
    [:span {:class "text-xs text-gray-300"}
     "Saved!"]]])
(defmethod ^:private saved-status-el :edited [_]
  [:div {:class "flex flex-row-reverse"}
   [:div {:class "flex items-center gap-small"}
    [:figure {:class "w-3"}
     [:img {:src "/icons/icon-loader-circle-white.svg"
            :class "animate-spin"}]]
    [:span {:class "text-xs text-gray-300 italic"}
     "Edited"]]])

(defn process-schema [tree schema-key prefix]
  (reduce
   (fn [acc table-key]
     (let [qualified-key (if prefix
                           (str schema-key "." table-key)
                           table-key)]
       (assoc acc qualified-key (keys (get (get (:schema-tree tree) schema-key) table-key)))))
   {}
   (keys (get (:schema-tree tree) schema-key))))

(defn convert-tree [tree]
  (let [schema-keys (keys (:schema-tree tree))]
    (cond
      (> (count schema-keys) 1) (reduce
                                 (fn [acc schema-key]
                                   (merge acc (process-schema tree schema-key true)))
                                 {}
                                 schema-keys)
      (<= (count schema-keys) 1) (process-schema tree (first schema-keys) false)
      :else #js{})))

(defn- editor []
  (let [user (rf/subscribe [:users->current-user])
        db-connections (rf/subscribe [:connections])
        run-connections-list (rf/subscribe [:editor-plugin->run-connection-list])
        filtered-run-connections-list (rf/subscribe [:editor-plugin->filtered-run-connection-list])
        run-connection-list-selected (rf/subscribe [:editor-plugin->run-connection-list-selected])
        database-schema (rf/subscribe [::subs/database-schema])
        plugins (rf/subscribe [:plugins->my-plugins])
        selected-template (rf/subscribe [:runbooks-plugin->selected-runbooks])
        script-response (rf/subscribe [:editor-plugin->script])
        vertical-pane-sizes (mapv js/parseInt
                                  (cs/split
                                   (or (.getItem js/localStorage "editor-vertical-pane-sizes") "250,950") ","))
        horizontal-pane-sizes (mapv js/parseInt
                                    (cs/split
                                     (or (.getItem js/localStorage "editor-horizontal-pane-sizes") "650,490") ","))
        script (r/atom (get-code-from-localstorage))
        select-theme (r/atom "dracula")
        metadata (r/atom [])
        metadata-key (r/atom "")
        metadata-value (r/atom "")
        languages-options [{:text "Shell" :value "command-line"}
                           {:text "MySQL" :value "mysql"}
                           {:text "Postgres" :value "postgres"}
                           {:text "SQL Server" :value "mssql"}
                           {:text "MongoDB" :value "mongodb"}
                           {:text "JavaScript" :value "nodejs"}
                           {:text "Python" :value "python"}
                           {:text "Ruby" :value "ruby-on-rails"}
                           {:text "Clojure" :value "clojure"}]
        theme-options [{:text "Dracula" :value "dracula"}
                       {:text "Nord" :value "nord"}
                       {:text "Sublime" :value "sublime"}
                       {:text "Github dark" :value "github-dark"}]]
    (rf/dispatch [:runbooks-plugin->clear-active-runbooks])
    (fn [{:keys [script-output]}]
      (let [is-mac? (>= (.indexOf (.toUpperCase (.-platform js/navigator)) "MAC") 0)
            is-one-connection-selected? (= 1 (count @run-connection-list-selected))
            last-connection-selected (last @run-connection-list-selected)
            feature-ai-ask (or (get-in @user [:data :feature_ask_ai]) "disabled")
            script-output-loading? (= (:status @script-output) :loading)
            get-plugin-by-name (fn [name] (first (filter #(= (:name %) name) @plugins)))
            review-plugin->connections (map #(:name %) (:connections (get-plugin-by-name "review")))
            current-connection last-connection-selected
            connection-name (:name current-connection)
            connection-type (discorver-connection-type current-connection)
            current-connection-details (fn [connection]
                                         (first (filter #(= (:name connection) (:name %))
                                                        (:connections (get-plugin-by-name "editor")))))
            schema-disabled? (fn [connection]
                               (or (= (first (:config (current-connection-details connection)))
                                      "schema=disabled")
                                   (= (:access_schema connection) "disabled")))
            run-connections-list-selected @run-connection-list-selected
            run-connections-list-rest (filterv #(and (not (:selected %))
                                                     (not= (:name %) connection-name))
                                               @filtered-run-connections-list)
            keymap [{:key "Mod-Enter"
                     :run (fn [_]
                            (submit-task
                             {}
                             @script
                             run-connections-list-selected
                             multiple-connections-exec-list-component/atom-exec-list-open?
                             (conj @metadata {:key @metadata-key :value @metadata-value})
                             script-response)

                            (reset! metadata [])
                            (reset! metadata-key "")
                            (reset! metadata-value ""))
                     :preventDefault true}
                    {:key "Mod-Shift-Enter"
                     :run (fn [^cm-state/StateCommand config]
                            (let [ranges (.-ranges (.-selection (.-state config)))
                                  from (.-from (first ranges))
                                  to (.-to (first ranges))]
                              (submit-task
                               {}
                               (.sliceString ^cm-state/Text (.-doc (.-state config)) from to)
                               run-connections-list-selected
                               multiple-connections-exec-list-component/atom-exec-list-open?
                               (conj @metadata {:key @metadata-key :value @metadata-value})
                               script-response)

                              (reset! metadata [])
                              (reset! metadata-key "")
                              (reset! metadata-value "")))
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
                    {:key "Alt-l" :mac "Ctrl-l" :run cm-commands/selectLine}
                    {:key "Mod-i" :run cm-commands/selectParentSyntax :preventDefault true}
                    {:key "Mod-[" :run cm-commands/indentLess :preventDefault true}
                    {:key "Mod-]" :run cm-commands/indentMore :preventDefault true}
                    {:key "Mod-Alt-\\" :run cm-commands/indentSelection}
                    {:key "Shift-Mod-k" :run cm-commands/deleteLine}
                    {:key "Shift-Mod-\\" :run cm-commands/cursorMatchingBracket}
                    {:key "Mod-/" :run cm-commands/toggleComment}
                    {:key "Alt-A" :run cm-commands/toggleBlockComment}]
            language-parser-case (let [subtype (:subtype (last run-connections-list-selected))
                                       databse-schema-sanitized (if (= (:status @database-schema) :success)
                                                                  @database-schema
                                                                  {:status :failure :raw "" :schema-tree []})
                                       schema (if (and is-one-connection-selected?
                                                       (= subtype (:type @database-schema)))
                                                #js{:schema (clj->js (convert-tree databse-schema-sanitized))}
                                                #js{})]
                                   (case subtype
                                     "postgres" [(sql
                                                  (.assign js/Object (.-dialect PostgreSQL)
                                                           schema))]
                                     "mysql" [(sql
                                               (.assign js/Object (.-dialect MySQL)
                                                        schema))]
                                     "mssql" [(sql
                                               (.assign js/Object (.-dialect MSSQL)
                                                        schema))]
                                     "oracledb" [(sql
                                                  (.assign js/Object (.-dialect PLSQL)
                                                           schema))]
                                     "command-line" [(.define cm-language/StreamLanguage cm-shell/shell)]
                                     "javascript" [(.define cm-language/StreamLanguage cm-javascript/javascript)]
                                     "nodejs" [(.define cm-language/StreamLanguage cm-javascript/javascript)]
                                     "mongodb" [(.define cm-language/StreamLanguage cm-javascript/javascript)]
                                     "ruby-on-rails" [(.define cm-language/StreamLanguage cm-ruby/ruby)]
                                     "python" [(.define cm-language/StreamLanguage cm-python/python)]
                                     "clojure" [(.define cm-language/StreamLanguage cm-clojure/clojure)]
                                     "" [(.define cm-language/StreamLanguage cm-shell/shell)]
                                     [(.define cm-language/StreamLanguage cm-shell/shell)]))
            theme-parser-map {"dracula" dracula
                              "nord" nord
                              "github-dark" githubDark
                              "sublime" sublime
                              "" dracula
                              nil dracula}
            show-tree? (fn [connection]
                         (and (or (= (:type connection) "mysql-csv")
                                  (= (:type connection) "postgres-csv")
                                  (= (:type connection) "mongodb")
                                  (= (:type connection) "postgres")
                                  (= (:type connection) "mysql")
                                  (= (:type connection) "sql-server-csv")
                                  (= (:type connection) "mssql")
                                  (= (:type connection) "oracledb")
                                  (= (:type connection) "database"))
                              (not (some #(= (:name connection) %) review-plugin->connections))))]
        [:div {:class "h-full flex flex-col"}
         [:div {:class "h-16 border border-gray-600 flex justify-end items-center gap-small px-4"}
          [:div {:class "flex items-center gap-small"}
           [:span {:class "text-xxs text-gray-500"}
            (str (if is-mac?
                   "(Cmd+Enter)"
                   "(Ctrl+Enter)"))]
           [:div {:class "flex"}
            [button/tailwind-primary {:text [:div {:class "flex items-center gap-small"}
                                             [:> hero-solid-icon/PlayIcon {:class "h-3 w-3 shrink-0 text-white"
                                                                           :aria-hidden "true"}]
                                             [:span "Run"]]
                                      :on-click (fn [res]
                                                  (submit-task
                                                   res
                                                   @script
                                                   run-connections-list-selected
                                                   multiple-connections-exec-list-component/atom-exec-list-open?
                                                   (conj @metadata {:key @metadata-key :value @metadata-value})
                                                   script-response)

                                                  (reset! metadata [])
                                                  (reset! metadata-key "")
                                                  (reset! metadata-value ""))
                                      :disabled (or script-output-loading?
                                                    (empty? run-connections-list-selected))
                                      :type "button"}]]]]
         [:> Allotment {:defaultSizes vertical-pane-sizes
                        :onDragEnd #(.setItem js/localStorage "editor-vertical-pane-sizes" (str %))}
          [:> (.-Pane Allotment) {:minSize 250}
           [aside/main {:show-tree? show-tree?
                        :current-connection current-connection
                        :atom-run-connections-list run-connections-list
                        :atom-filtered-run-connections-list filtered-run-connections-list
                        :run-connections-list-selected run-connections-list-selected
                        :run-connections-list-rest run-connections-list-rest
                        :metadata metadata
                        :metadata-key metadata-key
                        :metadata-value metadata-value
                        :schema-disabled? schema-disabled?}]]
          [:> Allotment {:defaultSizes horizontal-pane-sizes
                         :onDragEnd #(.setItem js/localStorage "editor-horizontal-pane-sizes" (str %))
                         :vertical true}
           (if (= (:status @selected-template) :ready)
             [:section {:class "relative h-full p-3"}
              [runbooks-form/main {:runbook @selected-template
                                   :preselected-connection (:name current-connection)
                                   :selected-connections (filter #(:selected %) (:data @run-connections-list))}]]

             (if (empty? (:results @db-connections))
               [quickstart/main]

               [:<>
                [:> CodeMirror/default {:value @script
                                        :height "100%"
                                        :className "h-full text-sm"
                                        :theme (get theme-parser-map @select-theme)
                                        :basicSetup #js{:defaultKeymap false}
                                        :extensions (clj->js
                                                     (concat
                                                      (when (and (= feature-ai-ask "enabled")
                                                                 is-one-connection-selected?)
                                                        [(inlineCopilot
                                                          (fn [prefix suffix]
                                                            (extensions/fetch-autocomplete
                                                             (:subtype (last run-connections-list-selected))
                                                             prefix
                                                             suffix
                                                             (:raw @database-schema))))])
                                                      [(.of cm-view/keymap (clj->js keymap))]
                                                      language-parser-case
                                                      (when (= (:status @selected-template) :ready)
                                                        [(.of (.-editable cm-view/EditorView) false)
                                                         (.of (.-readOnly cm-state/EditorState) true)])))
                                        :onUpdate #(auto-save % script)}]]))

           [log-area/main connection-type is-one-connection-selected? (show-tree? current-connection)]]]
         [:div {:class "border border-gray-600"}
          [:footer {:class "flex justify-between items-center p-small gap-small"}
           [:div {:class "flex items-center gap-small"}
            [saved-status-el @code-saved-status]
            (when (:execution_time (:data @script-output))
              [:div {:class "flex items-center gap-small"}
               [:> hero-solid-icon/ClockIcon {:class "h-4 w-4 shrink-0 text-white"
                                              :aria-hidden "true"}]
               [:span {:class "text-xs text-gray-300"}
                (str "Last execution time " (formatters/time-elapsed (:execution_time (:data @script-output))))]])]
           [:div {:class "flex items-center gap-regular"}
            [forms/select-editor {:on-change #(reset! select-theme (-> % .-target .-value))
                                  :selected (or @select-theme "")
                                  :options theme-options}]
            [forms/select-editor {:on-change #(rf/dispatch [:editor-plugin->set-select-language
                                                            (-> % .-target .-value)])
                                  :selected (or (cond
                                                  (not (cs/blank? (:subtype (last run-connections-list-selected)))) (:subtype (last run-connections-list-selected))
                                                  (not (cs/blank? (:icon_name (last run-connections-list-selected)))) (:icon_name (last run-connections-list-selected))
                                                  (= (:type (last run-connections-list-selected)) "custom") "command-line"
                                                  :else (:type (last run-connections-list-selected))) "")
                                  :options languages-options}]]]]

         (when @multiple-connections-exec-list-component/atom-exec-list-open?
           [multiple-connections-exec-list-component/main (map #(into {} {:connection-name (:name %)
                                                                          :script @script
                                                                          :metadata (metadata->json-stringify @metadata)
                                                                          :type (:type %)
                                                                          :subtype (:subtype %)
                                                                          :session-id nil
                                                                          :status :ready})
                                                               run-connections-list-selected)])]))))

(defn main []
  (let [script-response (rf/subscribe [:editor-plugin->script])]
    (rf/dispatch [:editor-plugin->clear-script])
    (rf/dispatch [:editor-plugin->clear-connection-script])
    (rf/dispatch [:ask-ai->clear-ai-responses])
    (rf/dispatch [:connections->get-connections])
    (rf/dispatch [:audit->clear-session])
    (rf/dispatch [:plugins->get-my-plugins])
    (fn []
      (clearLocalCache)
      (rf/dispatch [:editor-plugin->get-run-connection-list])
      [editor {:script-output script-response}])))
