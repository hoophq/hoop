(ns webapp.settings.views.main
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.tabs :as tabs]
            [webapp.settings.views.hoop-app :as hoop-app]
            [webapp.settings.views.users :as users]))

(defmulti ^:private selected-view identity)
(defmethod ^:private selected-view :Users [_]
  [users/main])

(defmethod ^:private selected-view "Hoop App" [_]
  [hoop-app/main])

(defmethod ^:private selected-view :default [_]
  [users/main])

(defn select-view-handler [tabs admin? app-running?]
  (let [selected-tab (r/atom (first tabs))]
    (fn []
      [:div
       (when (or app-running? admin?)
         [tabs/tabs {:on-change #(reset! selected-tab %)
                     :tabs tabs}])
       [selected-view @selected-tab admin?]])))

(defn main []
  (let [user (rf/subscribe [:users->current-user])
        app-running? @(rf/subscribe [:hoop-app->running?])
        admin? (-> @user :data :admin?)]
    (rf/dispatch [:users->get-user-groups])
    (rf/dispatch [:users->get-users])
    (rf/dispatch [:hoop-app->get-app-status])
    (fn []
      (let [tabs (cond
                   (and app-running? admin?) [:Users "Hoop App"]
                   admin? [:Users]
                   app-running? ["Hoop App"])]
        [:div {:class "bg-white h-full px-large py-regular"}
         [select-view-handler tabs admin? app-running?]]))))
