(ns webapp.webclient.events.metadata
  (:require [re-frame.core :as rf]))

;; Metadata events
(rf/reg-event-db
 :editor-plugin/add-metadata
 (fn [db [_ metadata]]
   (-> db
       (update-in [:editor-plugin :metadata] conj metadata))))

(rf/reg-event-db
 :editor-plugin/update-metadata-key
 (fn [db [_ value]]
   (assoc-in db [:editor-plugin :metadata-key] value)))

(rf/reg-event-db
 :editor-plugin/update-metadata-value
 (fn [db [_ value]]
   (assoc-in db [:editor-plugin :metadata-value] value)))

(rf/reg-event-db
 :editor-plugin/clear-metadata-inputs
 (fn [db [_]]
   (-> db
       (assoc-in [:editor-plugin :metadata-key] "")
       (assoc-in [:editor-plugin :metadata-value] ""))))

(rf/reg-event-db
 :editor-plugin/clear-all-metadata
 (fn [db [_]]
   (-> db
       (assoc-in [:editor-plugin :metadata] [])
       (assoc-in [:editor-plugin :metadata-key] "")
       (assoc-in [:editor-plugin :metadata-value] ""))))

(rf/reg-event-db
 :editor-plugin/update-metadata-at-index
 (fn [db [_ index field value]]
   (assoc-in db [:editor-plugin :metadata index field] value)))

;; Metadata subscriptions
(rf/reg-sub
 :editor-plugin/metadata
 (fn [db]
   (get-in db [:editor-plugin :metadata] [])))

(rf/reg-sub
 :editor-plugin/metadata-key
 (fn [db]
   (get-in db [:editor-plugin :metadata-key] "")))

(rf/reg-sub
 :editor-plugin/metadata-value
 (fn [db]
   (get-in db [:editor-plugin :metadata-value] "")))

