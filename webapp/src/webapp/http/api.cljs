(ns webapp.http.api
  (:require
   [webapp.http.request :as request]
   [webapp.config :as config]))

(defn request
  "request abstraction for calling Hoop API

  This functions receives one argument with the following keys:
  :method -> string of a http verb (GET, POST, PUT, DELETE, etc). If nil, defaults to GET
  :uri -> URI to be called
  :body -> a clojure map of the body structure
  :on-sucess -> callback that receives as argument the response payload
  :on-failure -> callback that has one argument that is the error message to treat 4xx and 5xx status codes. If not provided, a default callback will be called
  :on-unauthenticated -> a function to be called when the auth fails

  it returns a promise with the response in a clojure map and executes a on-sucess callback"
  [{:keys [method uri query-params body on-success on-failure headers]}]
  (let [token (.getItem js/sessionStorage "jwt-token")
        common-headers {:headers {:accept "application/json"
                                  "Content-Type" "application/json"
                                  "Authorization" (str "Bearer " token)
                                  ;; Overriding user-agent header doesn't seem to make
                                  ;; any effect in some browsers. Use a custom header instead.
                                  "User-Client" (str "webapp.core/" config/app-version)}}
        headers (merge
                 (:headers common-headers)
                 headers)]
    (request/request {:method method
                      :body body
                      :query-params (or query-params {})
                      :on-success on-success
                      :on-failure on-failure
                      :url (str config/api uri)
                      :options {:headers headers}})))

