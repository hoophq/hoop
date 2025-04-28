(ns webapp.webclient.codemirror.extensions
  (:require [webapp.config :as config]))

;; Debounce function to limit frequent API calls during typing
(def ^:private copilot-debounce-timer (atom nil))
(def ^:private copilot-cache (atom {}))
(def ^:private last-request (atom nil))

(defn- truncate-schema [schema max-size]
  (if (and schema (> (count schema) max-size))
    (subs schema 0 max-size)
    schema))

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
  (let [request-key (str language prefix suffix)
        is-typing (boolean (aget js/window "is_typing"))
        current-time (.now js/Date)
        debounce-delay 600]

    ;; Return cached result immediately if available and recent
    (when-let [cached (@copilot-cache request-key)]
      (when (< (- current-time (:timestamp cached)) 10000) ; Cache valid for 10 seconds
        (js/Promise.resolve (:result cached))))

    ;; Don't make new requests while typing rapidly or if a request was made very recently
    (when (and is-typing
               @last-request
               (< (- current-time @last-request) debounce-delay))
      (js/Promise.resolve nil))

    ;; Otherwise, proceed with API call
    (let [token (.getItem js/localStorage "jwt-token")
          common-headers {:headers {:accept "application/json"
                                    "Content-Type" "application/json"
                                    "Authorization" (str "Bearer " token)
                                    "User-Client" (str "webapp.core/" config/app-version)}}
          headers (:headers common-headers)
          ;; Limit schema size to prevent performance issues
          truncated-schema (truncate-schema database-schema 10000)
          request (clj->js {:method "POST"
                            :headers headers
                            :body (js/JSON.stringify (clj->js {:messages (messages language prefix suffix truncated-schema)
                                                               :model "gpt-4o-mini"}))})]

      ;; Update last request timestamp
      (reset! last-request current-time)

      ;; Clear any existing debounce timer
      (when @copilot-debounce-timer
        (js/clearTimeout @copilot-debounce-timer))

      ;; Set up new debounce timer
      (reset! copilot-debounce-timer
              (js/setTimeout
               (fn []
                 (-> (js/fetch (str config/api "/features/ask-ai/v1/chat/completions") request)
                     (.then #(.json %))
                     (.then #(js->clj % :keywordize-keys true))
                     (.then (fn [response]
                              (let [content (get-in response [:choices 0 :message :content])]
                                ;; Cache the result
                                (swap! copilot-cache assoc request-key
                                       {:result content
                                        :timestamp current-time})
                                content)))))
               (if is-typing debounce-delay 50))))))
