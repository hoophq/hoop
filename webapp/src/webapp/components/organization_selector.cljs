(ns webapp.components.organization-selector
  (:require [re-frame.core :as rf]
            [webapp.components.generic-selector :refer [generic-selector]]
            [webapp.events.core :as events]))

;; Events for fetching organizations
(rf/reg-event-fx
 :organization-selector/fetch-organizations
 (fn [{:keys [db]} [_ _]]
   {:fx [[:dispatch
          [:fetch {:method "GET"
                   :uri "/orgs/membership"
                   :on-success (fn [orgs]
                                 (rf/dispatch [:organization-selector/set-organizations orgs]))}]]]
    :db (assoc db :organization-selector/organizations {:loading true :data []})}))

;; Events for setting organizations
(rf/reg-event-fx
 :organization-selector/set-organizations
 (fn
   [{:keys [db]} [_ orgs]]
   {:db (assoc db :organization-selector/organizations {:loading false :data orgs})}))

;; Events for fetching active organization
(rf/reg-event-fx
 :organization-selector/fetch-active
 (fn [{:keys [db]} [_ _]]
   {:fx [[:dispatch
          [:fetch {:method "GET"
                   :uri "/orgs/active"
                   :on-success (fn [org]
                                 (rf/dispatch [:organization-selector/set-active org]))}]]]
    :db (assoc db :organization-selector/active {:loading true :data nil})}))

;; Events for setting active organization
(rf/reg-event-fx
 :organization-selector/set-active
 (fn
   [{:keys [db]} [_ org]]
   {:db (assoc db :organization-selector/active {:loading false :data org})}))

;; Events for switching organization
(rf/reg-event-fx
 :organization-selector/switch-organization
 (fn
   [{:keys [db]} [_ org-id]]
   {:fx [[:dispatch
          [:fetch {:method "POST"
                   :uri "/orgs/active"
                   :body {:organization_id org-id}
                   :on-success (fn [org]
                                 (rf/dispatch [:organization-selector/set-active org])
                                 (rf/dispatch [:events/fetch-main-resources]))}]]]}))

;; Subscriptions for organizations
(rf/reg-sub
 :organization-selector/organizations
 (fn [db]
   (:organization-selector/organizations db)))

;; Subscriptions for active organization
(rf/reg-sub
 :organization-selector/active
 (fn [db]
   (:organization-selector/active db)))

;; Component rendering function
(defn organization-selector []
  (let [organizations @(rf/subscribe [:organization-selector/organizations])
        active @(rf/subscribe [:organization-selector/active])]
    (when (and (not (:loading organizations))
               (not (:loading active))
               (seq (:data organizations)))
      [:div {:class "mb-4"}
       [generic-selector
        {:items (map (fn [org]
                       {:id (:id org)
                        :name (:name org)
                        :selected (= (:id org) (:id (:data active)))})
                     (:data organizations))
         :on-change #(rf/dispatch [:organization-selector/switch-organization %])
         :label "Organization"
         :button-class "org-selector bg-gray-800 text-white text-sm rounded-md px-3 py-1.5 hover:bg-gray-700 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-indigo-500"}]]))

;; Initialize function to load organizations on component mount
(defn initialize []
  (rf/dispatch [:organization-selector/fetch-organizations])
  (rf/dispatch [:organization-selector/fetch-active]))
