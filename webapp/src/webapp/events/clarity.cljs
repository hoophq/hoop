(ns webapp.events.clarity
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :clarity->verify-environment
 (fn [_ [_ user]]
   (let [hostname (.-hostname js/window.location)]
     (cond
       (= hostname "localhost") (.clarity js/window "stop")
       (= hostname "appdemo.hoop.dev") (.clarity js/window "stop")
       (= hostname "tryrunops.hoop.dev") (.clarity js/window "stop")
       (= (:org_name user) "hoopdev") (.clarity js/window "stop")
       :else (.clarity js/window "start")))
   {}))
