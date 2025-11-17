(ns webapp.events.segment
  (:require
   ["@segment/analytics-next" :refer [AnalyticsBrowser]]
   [re-frame.core :as rf]
   [webapp.config :as config]))

(rf/reg-event-fx
 :segment->load
 (fn [{:keys [db]} [_ event-callback]]
   (let [analytics-tracking (= "enabled" (get-in db [:gateway->info :data :analytics_tracking] "disabled"))]
     (if (not analytics-tracking)
       ;; If analytics tracking is disabled, don't load segment
       {}
       ;; Otherwise, load segment
       (let [segment-analytics (.load AnalyticsBrowser #js{:writeKey config/segment-write-key})]
         (merge
          {:db (assoc db :segment->analytics segment-analytics)}
          (when event-callback
            {:fx [[:dispatch event-callback]]})))))))

(rf/reg-event-fx
 :segment->identify
 (fn [{:keys [db]} [_ user-id traits]]
   (let [analytics-tracking (= "enabled" (get-in db [:gateway->info :data :analytics_tracking] "disabled"))]
     (if (not analytics-tracking)
       {}
       (let [analytics (-> db :segment->analytics)]
         (if (nil? analytics)
           {:fx [[:dispatch [:segment->load [:segment->identify user-id traits]]]]}
           (do
             (.identify analytics user-id (clj->js traits))
             {})))))))

(rf/reg-event-fx
 :segment->group
 (fn [{:keys [db]} [_ group-id traits]]
   (let [analytics-tracking (= "enabled" (get-in db [:gateway->info :data :analytics_tracking] "disabled"))]
     (if (not analytics-tracking)
       {}
       (let [analytics (-> db :segment->analytics)]
         (if (nil? analytics)
           {:fx [[:dispatch [:segment->load [:segment->group group-id traits]]]]}
           (do
             (.group analytics group-id (clj->js traits))
             {})))))))

(rf/reg-event-fx
 :segment->track
 (fn [{:keys [db]} [_ event-name properties]]
   (let [analytics-tracking (= "enabled" (get-in db [:gateway->info :data :analytics_tracking] "disabled"))]
     (if (not analytics-tracking)
       ;; If analytics tracking is disabled, don't send track events
       {}
       ;; Otherwise, send track events
       (let [analytics (-> db :segment->analytics)]
         (if (nil? analytics)
           {:fx [[:dispatch [:segment->load [:segment->track event-name properties]]]]}
           (do
             (.track analytics event-name (clj->js (merge
                                                    {:hostname (.-hostname js/location)}
                                                    properties)))
             {})))))))
