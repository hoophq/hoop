;; See the note in events.cljs: only the rule list read path survives the React
;; migration, kept for the CLJS AI Session Analyzer rule form.
(ns webapp.features.access-request.subs
  (:require
   [re-frame.core :as rf]))

(rf/reg-sub
 :access-request/rules
 (fn [db]
   (get-in db [:access-request :rules])))
