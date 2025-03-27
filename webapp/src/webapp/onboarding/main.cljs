(ns webapp.onboarding.main
  (:require [re-frame.core :as rf]
            [webapp.components.loaders :as loaders]))

(defn main []
  (let [user (rf/subscribe [:users->current-user])
        connections (rf/subscribe [:connections])
        check-performed? (atom false)]

    ;; Dispatch data loading
    (rf/dispatch [:users->get-user])
    (rf/dispatch [:connections->get-connections])

    (fn []
      (let [user-loading? (:loading @user)
            connections-loading? (:loading @connections)]

        ;; Only perform check when both data are loaded and check hasn't been performed yet
        (when (and (not user-loading?)
                   (not connections-loading?)
                   (not @check-performed?))
          (reset! check-performed? true)
          (rf/dispatch [:onboarding/check-user]))

        ;; Show loading while either data is still loading
        (if (or user-loading? connections-loading?)
          [:div.flex.items-center.justify-center.h-screen
           [loaders/simple-loader]]

          ;; Empty div after check is performed - component will be unmounted by navigation
          [:div])))))
