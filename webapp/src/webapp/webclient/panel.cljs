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
   ["@radix-ui/themes" :refer [Box Flex Spinner]]
   ["@uiw/codemirror-theme-material" :refer [materialDark materialLight]]
   ["@uiw/react-codemirror" :as CodeMirror]
   ["allotment" :refer [Allotment]]
   ["codemirror-copilot" :refer [clearLocalCache inlineCopilot]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [goog.object :as gobj]
   [webapp.formatters :as formatters]
   [webapp.subs :as subs]
   [webapp.components.keyboard-shortcuts :as keyboard-shortcuts]
   [webapp.webclient.codemirror.extensions :as extensions]
   [webapp.webclient.components.connections-list :as connections-list]
   [webapp.webclient.components.header :as header]
   [webapp.webclient.components.language-select :as language-select]
   [webapp.webclient.components.panels.connections :as connections-panel]
   [webapp.webclient.components.panels.metadata :as metadata-panel]
   [webapp.webclient.components.panels.runbooks :as runbooks-panel]
   [webapp.webclient.components.side-panel :refer [with-panel]]
   [webapp.webclient.exec-multiples-connections.exec-list :as multiple-connections-exec-list-component]
   [webapp.webclient.log-area.main :as log-area]
   [webapp.webclient.quickstart :as quickstart]
   [webapp.webclient.runbooks.form :as runbooks-form]))

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

(defn process-schema [tree schema-key prefix]
  (reduce
   (fn [acc table-key]
     (let [qualified-key (if prefix
                           (str schema-key "." table-key)
                           table-key)]
       (assoc acc qualified-key (keys (get (get (:schema-tree tree) schema-key) table-key)))))
   {}
   (keys (get (:schema-tree tree) schema-key))))

(def memoized-convert-tree
  (memoize
   (fn [tree max-tables]
     (println "aaaaa")
     (let [is-typing (boolean (aget js/window "is_typing"))
           schema-keys (keys (:schema-tree tree))
           limit-fn (fn [coll]
                      (if (and is-typing (> (count coll) max-tables))
                        (take max-tables coll)
                        coll))]
       (cond
         (> (count schema-keys) 1) (reduce
                                    (fn [acc schema-key]
                                      (let [tables (limit-fn (keys (get (:schema-tree tree) schema-key)))]
                                        (merge acc
                                               (reduce
                                                (fn [acc-inner table-key]
                                                  (let [qualified-key (str schema-key "." table-key)]
                                                    (assoc acc-inner qualified-key
                                                           (keys (get (get (:schema-tree tree) schema-key) table-key)))))
                                                {}
                                                tables))))
                                    {}
                                    schema-keys)
         (<= (count schema-keys) 1) (let [tables (limit-fn (keys (get (:schema-tree tree) (first schema-keys))))]
                                      (reduce
                                       (fn [acc table-key]
                                         (assoc acc table-key
                                                (keys (get (get (:schema-tree tree) (first schema-keys)) table-key))))
                                       {}
                                       tables))
         :else #js{})))))

(def ^:private timer (r/atom nil))
(def ^:private typing-intensity (r/atom 0))  ;; Track typing intensity
(def ^:private code-saved-status (r/atom :saved)) ; :edited | :saved
(def ^:private is-typing (r/atom false))
(def ^:private typing-timer (r/atom nil))

(defn update-global-typing-state [is-typing?]
  (reset! is-typing is-typing?)
  (aset js/window "is_typing" is-typing?))

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

;; Cache para armazenar schemas processados em formato JavaScript
(def schema-js-cache (r/atom {}))

;; Função para simplificar o schema apenas para autocompletar
(defn simplify-schema-for-autocomplete [schema]
  (let [schema-tree (:schema-tree schema)]
    (reduce-kv
     (fn [acc schema-name tables]
       (assoc acc schema-name
              (reduce-kv
               (fn [table-acc table-name columns]
                 (assoc table-acc table-name (vec (keys columns))))
               {}
               tables)))
     {}
     schema-tree)))

;; Criação do Web Worker para processamento assíncrono
(def schema-worker-blob
  "const processSchema = function(tree, maxTables, isTyping) {
    const schemaKeys = Object.keys(tree);

    // Função para limitar o número de itens conforme necessário
    const limitFn = (collection) => {
      if (isTyping && collection.length > maxTables) {
        return collection.slice(0, maxTables);
      }
      return collection;
    };

    // Sem schema
    if (schemaKeys.length === 0) {
      return {};
    }

    // Múltiplos schemas
    if (schemaKeys.length > 1) {
      let result = {};

      for (const schemaKey of schemaKeys) {
        const tables = tree[schemaKey] ? Object.keys(tree[schemaKey]) : [];
        const limitedTables = limitFn(tables);

        for (const tableKey of limitedTables) {
          const qualifiedKey = schemaKey + '.' + tableKey;
          const columns = tree[schemaKey][tableKey] || [];
          result[qualifiedKey] = columns;
        }
      }

      return result;
    }

    // Um único schema
    const schemaKey = schemaKeys[0];
    const tables = tree[schemaKey] ? Object.keys(tree[schemaKey]) : [];
    const limitedTables = limitFn(tables);

    let result = {};
    for (const tableKey of limitedTables) {
      const columns = tree[schemaKey][tableKey] || [];
      result[tableKey] = columns;
    }

    return result;
  };

  self.onmessage = function(e) {
    const { schema, maxTables, isTyping } = e.data;

    try {
      const processedSchema = processSchema(schema, maxTables, isTyping);

      self.postMessage({
        processedSchema: processedSchema
      });
    } catch (error) {
      self.postMessage({
        error: error.message
      });
    }
  };")

;; Função para inicializar o worker
(def schema-worker (atom nil))

(defn init-schema-worker []
  (when (and (nil? @schema-worker) (exists? js/Blob) (exists? js/URL) (exists? js/Worker))
    (let [blob (js/Blob. #js[schema-worker-blob] #js{:type "application/javascript"})
          url (js/URL.createObjectURL blob)]
      (reset! schema-worker (js/Worker. url)))))

;; Função para processar schema no worker
(defn process-schema-in-worker [schema max-tables is-typing?]
  (js/Promise.
   (fn [resolve reject]
     (if @schema-worker
       (let [handler (fn handler-fn [e]
                       (.removeEventListener @schema-worker "message" handler-fn)
                       (let [^js data (.-data e)]
                         (if (gobj/get data "error")
                           (reject (gobj/get data "error"))
                           (resolve (gobj/get data "processedSchema")))))]
         (.addEventListener @schema-worker "message" handler)
         (.postMessage @schema-worker #js{:schema (clj->js schema)
                                          :maxTables max-tables
                                          :isTyping is-typing?}))
       ;; Fallback caso o worker não esteja disponível
       (resolve (clj->js (memoized-convert-tree schema max-tables)))))))

;; Função otimizada para obter schema processado
(defn get-optimized-schema-for-codemirror [connection-name schema is-typing?]
  (let [cache-key [connection-name is-typing?]
        cached-value (get @schema-js-cache cache-key)
        max-tables (if is-typing? 20 100)]

    (if (and cached-value
             (= (:schema-version cached-value) (hash (:schema-tree schema))))
      ;; Retornar valor cacheado se schema não mudou
      (js/Promise.resolve (:schema-js cached-value))

      ;; Processar usando o worker ou fallback
      (-> (process-schema-in-worker
           (simplify-schema-for-autocomplete schema)
           max-tables
           is-typing?)
          (.then (fn [processed-schema]
                   (let [js-schema #js{:schema processed-schema}]
                     ;; Atualizar cache
                     (swap! schema-js-cache assoc cache-key
                            {:schema-version (hash (:schema-tree schema))
                             :schema-js js-schema})
                     js-schema)))))))

;; Inicializa o worker quando o módulo é carregado
(init-schema-worker)

;; Atom para armazenar o parser SQL atual e suas informações
(def current-sql-parser (r/atom nil))

;; Função para verificar se precisamos recriar o parser
(defn should-recreate-parser? [prev-lang current-lang prev-schema current-schema]
  (or (nil? prev-lang)
      (not= prev-lang current-lang)
      (not= (:status prev-schema) (:status current-schema))
      (and (= (:status current-schema) :success)
           (not= (:schema-tree prev-schema) (:schema-tree current-schema)))))

;; Função otimizada para criar ou reutilizar o parser SQL usando Web Worker
(defn get-or-create-sql-parser [current-language current-schema is-typing? is-one-connection?]
  (let [prev-parser-info (:info @current-sql-parser)
        prev-lang (:language prev-parser-info)
        prev-schema (:schema prev-parser-info)]

    (if (should-recreate-parser? prev-lang current-language prev-schema current-schema)
      ;; Só recria o parser se linguagem ou schema mudarem
      (let [database-schema-sanitized (if (= (:status current-schema) :success)
                                        current-schema
                                        {:status :failure :raw "" :schema-tree []})
            ;; Cria uma promessa para resolver o parser SQL
            parser-promise (if is-one-connection?
                             (get-optimized-schema-for-codemirror
                              (:name (:info @current-sql-parser))
                              database-schema-sanitized
                              is-typing?)
                             (js/Promise.resolve #js{}))
            ;; Cria um parser de fallback para usar enquanto processa o schema
            fallback-parser (case current-language
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

        ;; Usa o parser de fallback inicialmente
        (reset! current-sql-parser {:parser fallback-parser
                                    :info {:language current-language
                                           :schema current-schema}})

        ;; Atualiza o parser quando o schema for processado
        (.then parser-promise
               (fn [schema]
                 (let [new-parser (case current-language
                                    "postgres" [(sql (.assign js/Object (.-dialect PostgreSQL) schema))]
                                    "mysql" [(sql (.assign js/Object (.-dialect MySQL) schema))]
                                    "mssql" [(sql (.assign js/Object (.-dialect MSSQL) schema))]
                                    "oracledb" [(sql (.assign js/Object (.-dialect PLSQL) schema))]
                                    ;; Para outras linguagens, mantém o mesmo parser
                                    (:parser @current-sql-parser))]
                   (reset! current-sql-parser {:parser new-parser
                                               :info {:language current-language
                                                      :schema current-schema}}))))

        ;; Retorna o parser inicial enquanto processa em background
        fallback-parser)

      ;; Reutiliza o parser existente
      (:parser @current-sql-parser))))

;; Definir tempo de debounce para as operações após digitação
(def editor-debounce-time 750)

;; Otimização da função de atualização do estado de digitação
(defn update-global-typing-state-optimized [is-typing?]
  (when (not= @is-typing is-typing?)
    (reset! is-typing is-typing?)
    (aset js/window "is_typing" is-typing?)))

(defn editor []
  (let [user (rf/subscribe [:users->current-user])
        gateway-info (rf/subscribe [:gateway->info])
        db-connections (rf/subscribe [:connections])
        selected-connection (rf/subscribe [:connections/selected])
        multi-selected-connections (rf/subscribe [:connection-selection/selected])
        database-schema (rf/subscribe [::subs/database-schema])
        selected-template (rf/subscribe [:runbooks-plugin->selected-runbooks])
        multi-exec (rf/subscribe [:multi-exec/modal])

        active-panel (r/atom nil)
        multi-run-panel? (r/atom false)
        dark-mode? (r/atom (= (.getItem js/localStorage "dark-mode") "true"))

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
    (rf/dispatch [:runbooks-plugin->clear-active-runbooks])
    (rf/dispatch [:gateway->get-info])

    (fn [{:keys [script-output]}]
      (let [is-one-connection-selected? (= 0 (count @multi-selected-connections))
            feature-ai-ask (or (get-in @user [:data :feature_ask_ai]) "disabled")
            current-connection @selected-connection
            connection-name (:name current-connection)
            connection-type (discover-connection-type current-connection)
            disabled-download (-> @gateway-info :data :disable_sessions_download)
            reset-metadata (fn []
                             (reset! metadata [])
                             (reset! metadata-key "")
                             (reset! metadata-value ""))
            keymap [{:key "Mod-Enter"
                     :run (fn [_]
                            (rf/dispatch [:editor-plugin/submit-task {:script @script}]))
                     :preventDefault true}
                    {:key "Mod-Shift-Enter"
                     :run (fn [^cm-state/StateCommand config]
                            (let [ranges (.-ranges (.-selection (.-state config)))
                                  from (.-from (first ranges))
                                  to (.-to (first ranges))]
                              (rf/dispatch [:editor-plugin/submit-task
                                            {:script
                                             (.sliceString ^cm-state/Text (.-doc (.-state config)) from to)}])))
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
            current-schema (get-in @database-schema [:data connection-name])
            language-info @(rf/subscribe [:editor-plugin/language])
            current-language (or (:selected language-info) (:default language-info))

            ;; Substituir a criação direta do parser por nossa função otimizada
            language-parser-case (get-or-create-sql-parser
                                  current-language
                                  current-schema
                                  @is-typing
                                  is-one-connection-selected?)

            show-tree? (fn [connection]
                         (or (= (:type connection) "mysql-csv")
                             (= (:type connection) "postgres-csv")
                             (= (:type connection) "mongodb")
                             (= (:type connection) "postgres")
                             (= (:type connection) "mysql")
                             (= (:type connection) "sql-server-csv")
                             (= (:type connection) "mssql")
                             (= (:type connection) "oracledb")
                             (= (:type connection) "database")))
            panel-content (case @active-panel
                            :runbooks (runbooks-panel/main)
                            :metadata (metadata-panel/main {:metadata metadata
                                                            :metadata-key metadata-key
                                                            :metadata-value metadata-value})
                            nil)]

        (if (and (empty? (:results @db-connections))
                 (not (:loading @db-connections)))
          [quickstart/main]

          [:> Box {:class (str "h-full bg-gray-2 overflow-hidden "
                               (when @dark-mode?
                                 "dark"))}

           [header/main
            active-panel
            multi-run-panel?
            dark-mode?
            #(rf/dispatch [:editor-plugin/submit-task {:script @script}])]
           [with-panel
            (boolean @active-panel)
            [:> Box {:class "flex h-terminal-content overflow-hidden"}
             [:> Allotment {:defaultSizes vertical-pane-sizes
                            :onDragEnd #(.setItem js/localStorage "editor-vertical-pane-sizes" (str %))}
              [:> (.-Pane Allotment) {:minSize 270}
               [:aside {:class "h-full flex flex-col gap-8 border-r-2 border-[--gray-3] overflow-auto pb-16"}
                (if @multi-run-panel?
                  [connections-panel/main dark-mode?]
                  [connections-list/main dark-mode? (show-tree? current-connection)])]]

              [:> Allotment {:defaultSizes horizontal-pane-sizes
                             :onDragEnd #(.setItem js/localStorage "editor-horizontal-pane-sizes" (str %))
                             :vertical true}
               (if (= (:status @selected-template) :ready)
                 [:section {:class "relative h-full p-3 overflow-auto"}
                  [runbooks-form/main {:runbook @selected-template
                                       :preselected-connection (:name current-connection)
                                       :selected-connections (conj @multi-selected-connections current-connection)}]]

                 [:> CodeMirror/default
                  {:value @script
                   :height "100%"
                   :className "h-full text-sm"
                   :theme (if @dark-mode?
                            materialDark
                            materialLight)
                   :basicSetup #js{:defaultKeymap false}

                   ;; Otimizar o onChange para usar um único timer
                   :onChange (fn [value _]
                               (reset! script value)
                               (reset! code-saved-status :edited)
                               (update-global-typing-state-optimized true)
                               (when @typing-timer (js/clearTimeout @typing-timer))
                               (reset! typing-timer
                                       (js/setTimeout
                                        (fn []
                                          (update-global-typing-state-optimized false)
                                          (save-code-to-localstorage value))
                                        editor-debounce-time)))

                   :extensions (clj->js
                                (concat
                                 (when (and (= feature-ai-ask "enabled")
                                            is-one-connection-selected?)
                                   [(inlineCopilot
                                     #js{:getSuggestions (fn [prefix suffix]
                                                           (extensions/fetch-autocomplete
                                                            (:subtype current-connection)
                                                            prefix
                                                            suffix
                                                            (:raw current-schema)))
                                         :debounceMs 800
                                         :maxPrefixLength 500
                                         :maxSuffixLength 500})])
                                 [(.of cm-view/keymap (clj->js keymap))]
                                 language-parser-case
                                 (when (= (:status @selected-template) :ready)
                                   [(.of (.-editable cm-view/EditorView) false)
                                    (.of (.-readOnly cm-state/EditorState) true)])))}])

               [:> Flex {:direction "column" :justify "between" :class "h-full"}
                [log-area/main
                 connection-type
                 is-one-connection-selected?
                 (show-tree? current-connection)
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
            panel-content]

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

(defn main []
  (let [script-response (rf/subscribe [:editor-plugin->script])]
    (rf/dispatch [:editor-plugin->clear-script])
    (rf/dispatch [:editor-plugin->clear-connection-script])
    (rf/dispatch [:ask-ai->clear-ai-responses])
    (rf/dispatch [:connections->get-connections])
    (rf/dispatch [:audit->clear-session])
    (rf/dispatch [:plugins->get-my-plugins])
    (rf/dispatch [:jira-templates->get-all])
    (rf/dispatch [:jira-integration->get])
    (rf/dispatch [:search/clear-term])
    (fn []
      (clearLocalCache)
      (rf/dispatch [:editor-plugin->get-run-connection-list])
      [editor {:script-output script-response}])))
