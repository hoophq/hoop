(ns webapp.events.components.dialog
  (:require
   [re-frame.core :as rf]))

(rf/reg-event-fx
 :dialog->clear
 (fn [{:keys [db]} [_ _]]
   {:db (assoc-in db [:dialog] {:status :closed
                                :on-success nil
                                :type :info
                                :text ""
                                :text-action-button ""
                                :action-button? true
                                :title ""})}))

(rf/reg-event-fx
 :dialog->set-status
 (fn [{:keys [db]} [_ status]]
   {:db (assoc-in db [:dialog :status] (if (= status :open)
                                         true
                                         false))}))

(rf/reg-event-fx
 :dialog->close
 (fn [{:keys [db]} [_ _]]
   (js/setTimeout #(rf/dispatch [:dialog->clear]) 500)
   {:db (assoc-in db [:dialog :status] :closed)}))

(rf/reg-event-fx
 :dialog->open
 (fn [{:keys [db]} [_ data]]
   {:db (assoc-in db [:dialog] {:status :open
                                :type (:type data)
                                :on-success (:on-success data)
                                :text (:text data)
                                :action-button? (:action-button? data)
                                :text-action-button (:text-action-button data)
                                :title (:title data)})}))
