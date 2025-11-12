(ns webapp.http.request
  (:require
   [re-frame.core :as rf]))

(defn error-handling
  [error]
  (rf/dispatch [:show-snackbar {:level :error
                                :text (:message error)
                                :details error}]))

(defn not-ok
  "This functions has two possible outcomes:
  1 - When the status is 401 (Unauthorized), it redirects user to the logout page
  2 - when the status is 399 or below, it executes a on-failure function, that is provided by upperscope."
  [{:keys [status on-failure]}]
  (when (= status 401)
    (rf/dispatch [:navigate :logout-hoop]))
  (when (> status 399) (on-failure)))

(defn parse-response
  [response on-success on-failure]
  (let [status (.-status response)]
    ;; Clone response to check body first
    (.then
     (.text (.clone response))
     (fn [text]
       (cond
         ;; No content in body
         (empty? text)
         (if (.-ok response)
           (on-success nil (.-headers response))
           (not-ok {:status status
                    :on-failure #(on-failure {:status status})}))

         ;; Has content - try JSON first
         :else
         (.then
          (.json response)
          (fn [json]
            (let [payload (js->clj json :keywordize-keys true)]
              (if (.-ok response)
                (on-success payload (.-headers response))
                (not-ok {:status status
                         :on-failure #(on-failure payload)}))))
          (fn [_error]
            ;; JSON failed, return as text
            (if (.-ok response)
              (on-success text (.-headers response))
              (not-ok {:status status
                       :on-failure #(on-failure {:message text :status status})})))))))))

(defn query-params-parser
  [queries]
  (let [url-search-params (new js/URLSearchParams (clj->js queries))]
    (if (and (seq (.toString url-search-params)) queries)
      (str "?" (.toString url-search-params))
      "")))

(defn handle-response
  [method response on-success on-failure]
  (if (= method "HEAD")
    ;; HEAD requests only need headers, no body parsing
    (do
      (when (.-ok response)
        (on-success response (.-headers response)))
      response)
    ;; Parse response intelligently based on content
    (parse-response response on-success on-failure)))

(defn request
  "request abstraction for making a http request

  This functions receives one argument with the following keys:
  :method -> string of a http verb (GET, POST, PUT, DELETE, etc). If nil, defaults to GET
  :url -> URL to be called
  :body -> a clojure map of the body structure
  :on-sucess -> callback that receives as argument the response payload
  :on-failure -> callback that receives the complete error object (not just the :message)
  :options -> this is a map of options, like headers

  it returns a promise with the response in a clojure map and executes a on-sucess callback"
  [{:keys [method url body query-params on-success on-failure options]}]
  (let [json-body (.stringify js/JSON (clj->js body))
        actual-on-failure (or on-failure error-handling)]
    (.catch
     (.then
      (js/fetch (str url (query-params-parser query-params))
                (clj->js (merge options
                                {:method (or method "GET")}
                                (when-let [_ (and (not= method "GET")
                                                  (not= method "HEAD"))]
                                  {:body json-body}))))
      (fn [response]
        (handle-response method response on-success actual-on-failure)))
     (fn [error]
       (let [error-payload (if (= (.-message error) "Failed to fetch")
                             {:message "Network error: Failed to fetch" :type "network-error"}
                             (try
                               (js->clj (js/JSON.parse (.-message error)) :keywordize-keys true)
                               (catch js/Error _
                                 {:message (.-message error) :type "unknown-error"})))]
         (actual-on-failure error-payload))))))

