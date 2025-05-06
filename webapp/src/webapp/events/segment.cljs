(ns webapp.events.segment
  (:require
   ["@segment/analytics-next" :refer [AnalyticsBrowser]]
   [re-frame.core :as rf]
   [webapp.config :as config]))

(rf/reg-event-fx
 :segment->load
 (fn [{:keys [db]} [_ event-callback]]
   (let [analytics-tracking @(rf/subscribe [:gateway->analytics-tracking])]
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
 (fn [{:keys [db]} [_ user]]
   (let [analytics-tracking @(rf/subscribe [:gateway->analytics-tracking])]
     (if (not analytics-tracking)
       ;; If analytics tracking is disabled, don't send identify events
       {}
       ;; Otherwise, send identify events
       (let [user-id (:id user)
             analytics (-> db :segment->analytics)]
         (if (nil? analytics)
           {:fx [[:dispatch [:segment->load [:segment->identify user]]]]}
           (do
             (.identify analytics user-id (clj->js user))
             {})))))))

(rf/reg-event-fx
 :segment->track
 (fn [{:keys [db]} [_ event-name properties]]
   (let [analytics-tracking @(rf/subscribe [:gateway->analytics-tracking])]
     (if (not analytics-tracking)
       ;; If analytics tracking is disabled, don't send track events
       {}
       ;; Otherwise, send track events
       (let [analytics (-> db :segment->analytics)
             user (-> db :users->current-user :data)]
         (if (nil? analytics)
           {:fx [[:dispatch [:segment->load [:segment->track event-name properties]]]]}
           (do
             (.track analytics event-name (clj->js (merge
                                                    {:hostname (.-hostname js/location)}
                                                    user
                                                    properties)))
             {})))))))

