(ns webapp.webclient.codemirror.extensions
  (:require [webapp.config :as config]))

;; messages
;;
;; Generates a message structure for the OpenAI API based on the given parameters.
;;
;; Parameters:
;; - language: A string representing the programming language or database type.
;;   Possible values:
;;   "postgres", "mysql", "mssql", "oracledb", "command-line", "javascript",
;;   "nodejs", "mongodb", "ruby-on-rails", "python", "clojure", or "" (empty string)
;; - prefix: A string containing the code before the cursor position
;; - suffix: A string containing the code after the cursor position
;; - database-schema: An optional string containing the database schema (if applicable)
;;
;; Returns:
;; A vector of maps containing the system and user messages for the OpenAI API.
;;
;; The function creates a system message that instructs the AI to act as a programmer
;; in the specified language, and a user message that includes the code context.
(defn messages [language prefix suffix database-schema]
  [{:role "system"
    :content (str "You are a " language " programmer that replaces <FILL_ME> part with the "
                  "right code for a " language " single-file script. Only output the code that replaces <FILL_ME> part. "
                  "Do not add any explanation or markdown."
                  (when database-schema
                    (str "When the language is a query language, use this schema to help you: "
                         database-schema)))}
   {:role "user"
    :content (if (and (empty? prefix) (empty? suffix))
               "select <FILL_ME>"
               (str prefix "<FILL_ME>" suffix))}])

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