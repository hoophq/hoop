(ns webapp.webclient.log-area.main
  (:require ["@heroicons/react/16/solid" :as hero-micro-icon]
            ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["papaparse" :as papa]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.data-grid-table :as data-grid-table]
            [webapp.subs :as subs]
            [webapp.webclient.log-area.output-tabs :refer [tabs]]
            [webapp.webclient.log-area.logs :as logs]))

(defn- transform-results->matrix
  [results connection-type]
  (let [res (if (= connection-type "oracledb")
              (cs/join "\n" (drop 1 (cs/split results #"\n")))
              results)]
    (when-not (nil? results)
      (get (js->clj (papa/parse res (clj->js {"delimiter" "\t"}))) "data"))))

(def selected-tab (r/atom (or (.getItem js/localStorage "webclient-selected-tab")
                              "Logs")))

(defn- clean-postgres-script [script]
  (let [lines (cs/split script #"\n")]
    (if (and (> (count lines) 3)
             (= (first lines) "\\set QUIET on"))
      (cs/join "\n" (drop 3 lines))  ;; Pula as 3 primeiras linhas
      script)))

(defn main [_]
  (let [user (rf/subscribe [:users->current-user])
        script-response (rf/subscribe [:editor-plugin->script])
        question-responses (rf/subscribe [:ask-ai->question-responses])
        database-schema (rf/subscribe [::subs/database-schema])
        input-question (r/atom "")]
    (fn [connection-type connection-name is-one-connection-selected? show-tabular?]
      (let [current-schema (get-in @database-schema [:data connection-name])
            logs-content (mapv #(into {} {:status (:status %)
                                          :response (:output (:data %))
                                          :response-status (:output_status (:data %))
                                          :script (if (= connection-type "postgres")
                                                    (clean-postgres-script (:script (:data %)))
                                                    (:script (:data %)))
                                          :response-id (:session_id (:data %))
                                          :has-review (:has_review (:data %))
                                          :execution-time (:execution_time (:data %))
                                          :classes "h-full"}) @script-response)
            feature-ai-ask (or (get-in @user [:data :feature_ask_ai]) "disabled")
            ai-content (map #(into {} {:status (:status %)
                                       :script (:question (:data %))
                                       :response (:response (:data %))
                                       :response-id (:id (:data %))
                                       :execution-time (:execution_time (:data %))
                                       :classes "h-full"}) @question-responses)
            tabular-data (:data (first @script-response))
            tabular-status (:status (first @script-response))
            tabular-loading? (= tabular-status :loading)
            sanitize-results (when-not (nil? (:output tabular-data))
                               (cs/replace (:output tabular-data) #"∞" "\t"))
            results-transformed (transform-results->matrix sanitize-results connection-type)
            results-heads (first results-transformed)
            results-body (next results-transformed)
            connection-type-database? (some (partial = connection-type)
                                            ["mysql" "postgres" "sql-server" "oracledb" "mssql" "database"])
            available-tabs (merge
                            {:logs "Logs"}
                            (when (and connection-type-database?
                                       is-one-connection-selected?
                                       show-tabular?)
                              {:tabular "Tabular"})
                            (when (and (= feature-ai-ask "enabled")
                                       connection-type-database?
                                       is-one-connection-selected?)
                              {:ai "AI"}))]

        (when-not (some #(= @selected-tab %) (vals available-tabs))
          (.setItem js/localStorage "webclient-selected-tab" (first (vals available-tabs)))
          (reset! selected-tab (first (vals available-tabs))))

        [:div {:class "h-full flex flex-col"}
         ;; start ask-ai ui
         (when (and (= feature-ai-ask "enabled")
                    connection-type-database?
                    is-one-connection-selected?)
           [:div {:class "flex rounded-md shadow-sm"}
            [:form {:class "relative flex flex-grow items-stretch focus-within:z-10 border-gray-600"
                    :on-submit (fn [e]
                                 (.preventDefault e)
                                 (rf/dispatch [:ask-ai->ask-sql-question (:raw current-schema) @input-question])
                                 (reset! selected-tab "AI")
                                 (reset! input-question ""))}
             [:div {:class "pointer-events-none absolute inset-y-0 left-0 flex items-center pl-3"}
              [:> hero-micro-icon/SparklesIcon {:class "h-5 w-5 text-purple-400" :aria-hidden "true"}]]
             [:input {:type "text"
                      :name "ask-ai-question"
                      :id "ask-ai-question"
                      :disabled false
                      :auto-complete "off"
                      :on-change #(reset! input-question (-> % .-target .-value))
                      :class (str "block w-full py-1.5 bg-gray-900 "
                                  "pl-10 text-white border border-gray-600 border-r-0 "
                                  "placeholder:text-gray-400 sm:text-xs "
                                  "focus:ring-1 focus:ring-gray-400 focus:border-gray-400 focus:outline-none")
                      :placeholder "Ask AI anything"
                      :value @input-question}]
             [:button {:type "submit"
                       :class (str "border border-gray-600 border-l-0 px-3 py-2 text-sm font-semibold")}
              [:> hero-solid-icon/ArrowRightCircleIcon {:class "h-5 w-5 text-white" :aria-hidden "true"}]]]])
         ;;end ask-ai ui

         [:div {:class (str (if (= feature-ai-ask "enabled")
                              "h-logs-container"
                              "h-full"))}
          [tabs {:on-click (fn [_ value]
                             (.setItem js/localStorage "webclient-selected-tab" value)
                             (reset! selected-tab value))
                 :tabs available-tabs
                 :selected-tab @selected-tab}]
          (case @selected-tab
            "AI" [logs/main :ai ai-content]
            "Tabular" [data-grid-table/main results-heads results-body true tabular-loading?]
            "Logs" [logs/main :logs logs-content]
            :else [logs/main logs-content])]]))))
