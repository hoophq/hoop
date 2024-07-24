(ns webapp.webclient.codemirror.extensions
  (:require [webapp.config :as config]))

(defn messages [language prefix suffix database-schema]
  [{:role "system"
    :content (str "You are a " language " programmer that replaces <FILL_ME> part with the "
                  "right code. Only output the code that replaces <FILL_ME> part. "
                  "Do not add any explanation or markdown."
                  (when database-schema
                    (str "When the language is a query language, use this schema to help you: "
                         database-schema)))}
   {:role "user"
    :content (str prefix "<FILL_ME>" suffix)}])

(defn fetch-autocomplete [language prefix suffix database-schema]
  (let [token (.getItem js/localStorage "jwt-token")
        common-headers {:headers {:accept "application/json"
                                  "Content-Type" "application/json"
                                  "Authorization" (str "Bearer " token)
                                  "User-Client" (str "webapp.core/" config/app-version)}}
        headers (:headers common-headers)
        request (clj->js {:method "POST"
                          :headers headers
                          :body (js/JSON.stringify (clj->js {:messages (messages language prefix suffix database-schema)
                                                             :model "gpt-4o-mini"}))})]
    (-> (js/fetch (str config/api "/features/ask-ai/v1/chat/completions") request)
        (.then #(.json %))
        (.then #(js->clj % :keywordize-keys true))
        (.then #(get-in % [:choices 0 :message :content])))))
