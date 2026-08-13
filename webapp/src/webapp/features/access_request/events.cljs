;; The Access Request pages live in React (webapp_v2 pages/Features/AccessRequest).
;; What survives here is the rule list read path, still consumed by the CLJS AI
;; Session Analyzer rule form to populate its access request rule picker. Both
;; this namespace and subs.cljs can go once that page is migrated (EVL-148).
(ns webapp.features.access-request.events
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :access-request/list-rules
 (fn [_ [_]]
   {:fx [[:dispatch [:fetch {:method "GET"
                             :uri "/access-requests/rules"
                             :on-success (fn [response]
                                           (rf/dispatch [:access-request/set-rules (:data response)]))
                             :on-failure (fn [error]
                                           (rf/dispatch [:show-snackbar {:level :error
                                                                         :text "Failed to load access request rules"
                                                                         :details error}]))}]]]}))

(rf/reg-event-db
 :access-request/set-rules
 (fn [db [_ rules]]
   (assoc-in db [:access-request :rules] rules)))
