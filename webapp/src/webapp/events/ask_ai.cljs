(ns webapp.events.ask-ai
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :ask-ai->set-config
 (fn [_ [_ status]]
   {:fx [[:dispatch [:fetch
                     {:method "PUT"
                      :uri "/orgs/features"
                      :body {:name "ask-ai",
                             :status status}
                      :on-success (fn [_]
                                    (rf/dispatch [:users->get-user])
                                    (rf/dispatch [:show-snackbar {:level :success
                                                                  :text "The Ask-AI configs were updated!"}]))}]]]}))

(defn gpt-ai-message-model [database-schema question database-type]
  [{:role "system",
    :content (str "You are an engineer expert in SQL and NoSQL queries and helpful."
                  "\nGiven the following SQL or NoSQL schema, your job is to write queries given a user’s request.\n"
                  "Rules:\n"
                  "1. You must use the schema provided, and you can assume that the schema is correct.\n"
                  "2. You must write a valid SQL or NoSQL query in " database-type " dialect that answers the user’s question.\n"
                  "3. You must write a query that is as simple as possible, but no simpler.\n"
                  "4. You must try to complete the response in 500 characters or less.\n"
                  database-schema)},
   {:role "user",
    :content question}])

(rf/reg-event-fx
 :ask-ai->ask-sql-question
 (fn [{:keys [db]} [_ database-schema question database-type]]
   (let [on-success (fn [res]
                      (let [question-response (get-in res [:choices 0 :message :content])
                            id (get-in res [:id])
                            created (get-in res [:created])]
                        (rf/dispatch
                         [:show-snackbar {:level :success
                                          :text "Question made!"}])
                        (rf/dispatch [::ask-ai->set-response-success {:id id
                                                                      :question question
                                                                      :response question-response
                                                                      :execution_time (- (.now js/Date) (* created 1000))}])))
         on-failure (fn [error]
                      (rf/dispatch [:show-snackbar {:text error :level :error}])
                      (rf/dispatch [::ask-ai->set-response-failure error]))
         get-app-status [:fetch
                         {:method "POST"
                          :uri "/features/ask-ai/v1/chat/completions"
                          :body {:model "gpt-4o"
                                 :messages (gpt-ai-message-model database-schema question database-type)
                                 :temperature 0
                                 :max_tokens 500
                                 :top_p 1
                                 :frequency_penalty 0
                                 :presence_penalty 0}
                          :on-success on-success
                          :on-failure on-failure}]]
     {:db (assoc db :ask-ai->question-responses (into [] (cons {:status :loading :data nil}
                                                               (:ask-ai->question-responses db))))
      :fx [[:dispatch get-app-status]]})))

(rf/reg-event-fx
 ::ask-ai->set-response-success
 (fn
   [{:keys [db]} [_ data]]
   {:db (assoc db :ask-ai->question-responses (take 10
                                                    (assoc (:ask-ai->question-responses db) 0
                                                           {:status :success :data data})))}))

(rf/reg-event-fx
 ::ask-ai->set-response-failure
 (fn
   [{:keys [db]} [_ error]]
   {:db (assoc db :ask-ai->question-responses (take 10
                                                    (assoc (:ask-ai->question-responses db) 0
                                                           {:status :failure :data error})))}))

(rf/reg-event-fx
 :ask-ai->clear-ai-responses
 (fn
   [{:keys [db]} [_]]
   {:db (assoc db :ask-ai->question-responses [])}))

