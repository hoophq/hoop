(ns webapp.shared-ui.sidebar.constants
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]))

(def routes [{:name "Home"
              :label "Home"
              :icon (fn [props]
                      [:> hero-outline-icon/CodeBracketSquareIcon props])
              :uri "/client"
              :navigate :editor-plugin
              :free-feature? true
              :admin-only? false}
             {:name "Dashboard"
              :label "Dashboard"
              :icon (fn [props]
                      [:> hero-outline-icon/RectangleGroupIcon props])
              :uri "/dashboard"
              :navigate :dashboard
              :free-feature? false
              :admin-only? true}
             {:name "Sessions"
              :icon (fn [props]
                      [:> hero-outline-icon/RectangleStackIcon props])
              :uri "/sessions"
              :free-feature? true
              :admin-only? false}])

(def plugins-routes [{:name "review"
                      :label "Reviews"
                      :icon (fn [props]
                              [:> hero-outline-icon/InboxIcon props])
                      :uri "/reviews"
                      :free-feature? true
                      :navigate :reviews-plugin
                      :admin-only? false}])

(def plugins-management [{:name "webhooks"
                          :label "Webhooks"
                          :free-feature? false}
                         {:name "access_control"
                          :label "Access Control"
                          :free-feature? true}
                         {:name "runbooks"
                          :label "Runbooks"
                          :free-feature? true}
                         {:name "audit"
                          :label "Audit"
                          :free-feature? true}
                         {:name "slack"
                          :label "Slack"
                          :free-feature? true}])
