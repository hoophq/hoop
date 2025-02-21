;; webapp.webclient.events.codemirror
(ns webapp.webclient.events.codemirror
  (:require
   [re-frame.core :as rf]
   [clojure.string :as cs]))

(defn determine-default-language [connection]
  (cond
    ;; Database types
    (= (:type connection) "database") (or (:subtype connection) "postgres")

    ;; Application types
    (= (:type connection) "application") (or (:subtype connection) "command-line")

    ;; Custom types
    (= (:type connection) "custom") "command-line"

    ;; Specific subtypes take precedence
    (not (cs/blank? (:subtype connection))) (:subtype connection)

    ;; Icon name as fallback
    (not (cs/blank? (:icon_name connection))) (:icon_name connection)

    ;; Default fallback
    :else "command-line"))

;; Language events
(rf/reg-event-db
 :editor-plugin/set-language
 (fn [db [_ language]]
   (assoc-in db [:editor :language :selected] language)))

(rf/reg-event-db
 :editor-plugin/clear-language
 (fn [db _]
   (update-in db [:editor :language] dissoc :selected)))

;; Language subscription
(rf/reg-sub
 :editor-plugin/language
 (fn [db [_]]
   (let [connection (get-in db [:editor :connections :selected])
         manual-selection (get-in db [:editor :language :selected])
         default-language (determine-default-language connection)]
     {:selected manual-selection
      :default default-language})))
