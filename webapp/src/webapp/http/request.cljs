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
  [{:keys  [status on-failure]}]
  (when (= status 401)
    (rf/dispatch [:navigate :logout-hoop]))
  (when (> status 399) (on-failure)))

(defmulti response-parser identity)
(defmethod response-parser "application/json" [_ response on-success]
  (.then
   (.json response)
   (fn [json]
     (let [payload (js->clj json :keywordize-keys true)]
       (when (not (.-ok response))
         (not-ok {:status (.-status response)
                  :on-failure #(throw (js/Error. (js/JSON.stringify json)))}))
       (on-success payload (.-headers response))
       payload))

   (fn [json]
     (let [payload (js->clj json :keywordize-keys true)]
       (if (not (.-ok response))
         (not-ok {:status (.-status response)
                  :on-failure #(throw (js/Error. payload))})
         (on-success payload (.-headers response)))))))

;;TODO send headers object as second param of on-success and on-failure
(defmethod response-parser :default [_ response on-success]
  (.then
   (.text response)
   (fn [text]
     (when (not (.-ok response)) (not-ok {:status (.-status response)
                                          :on-failure #(throw (js/Error. text))}))
     (on-success text (.-headers response))
     text)

   (fn [json]
     (let [payload (js->clj json :keywordize-keys true)]
       (if (not (.-ok response))
         (not-ok {:status (.-status response)
                  :on-failure #(throw (js/Error. payload))})
         (on-success payload (.-headers response)))))))

(defn query-params-parser
  [queries]
  (let [url-search-params (new js/URLSearchParams (clj->js queries))]
    (if (and (not (empty? (.toString url-search-params))) queries)
      (str "?" (.toString url-search-params))
      "")))

(defmulti response-by-method (fn [method _response _on-success _on-failure] method))

(defmethod response-by-method "HEAD" [_ response on-success _]
  ;; HEAD requests only need headers, no body parsing
  (when (.-ok response)
    (on-success response (.-headers response)))
  response)

(defmethod response-by-method :default [_ response on-success on-failure]
  (let [content-type (.. response -headers (get "content-type"))]
    (if (and content-type (re-find #"application/json" content-type))
      (response-parser "application/json" response on-success on-failure)
      (response-parser :default response on-success on-failure))))

(defn request
  "request abstraction for making a http request

  This functions receives one argument with the following keys:
  :method -> string of a http verb (GET, POST, PUT, DELETE, etc). If nil, defaults to GET
  :url -> URL to be called
  :body -> a clojure map of the body structure
  :on-sucess -> callback that receives as argument the response payload
  :on-failure -> callback that has one argument that is the error message to treat 4xx and 5xx status codes. If not provided, a default callback will be called
  :options -> this is a map of options, like headers

  it returns a promise with the response in a clojure map and executes a on-sucess callback"
  [{:keys [method url body query-params on-success on-failure options]}]
  (let [json-body (.stringify js/JSON (clj->js body))]
    (.catch
     (.then
      (js/fetch (str url (query-params-parser query-params))
                (clj->js (merge options
                                {:method (or method "GET")}
                                (when-let [_ (and (not= method "GET")
                                                  (not= method "HEAD"))]
                                  {:body json-body}))))
      (fn [response]
        (response-by-method method response on-success on-failure)))
     (fn [error]
       (if (= (.-message error) "Failed to fetch")
         (if (= on-failure nil)
           (error-handling (js/JSON.parse (.-message error)))
           (on-failure (:message (.-message error))))

         (let [error-res (js/JSON.parse (.-message error))
               error-edn (js->clj error-res :keywordize-keys true)]
           (if (= on-failure nil)
             (error-handling error-edn)
             (on-failure (:message error-edn) error-edn))))))))

