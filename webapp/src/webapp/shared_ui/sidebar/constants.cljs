(ns webapp.shared-ui.sidebar.constants
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]))

(def routes [{:name "Home"
              :label "Home"
              :icon (fn [props]
                      [:> hero-outline-icon/CodeBracketSquareIcon props])
              :uri "/client"
              :navigate :editor-plugin
              :free-feature? true}
             {:name "Dashboard"
              :label "Dashboard"
              :icon (fn [props]
                      [:> hero-outline-icon/RectangleGroupIcon props])
              :uri "/dashboard"
              :navigate :dashboard
              :free-feature? true}
             {:name "Sessions"
              :icon (fn [props]
                      [:> hero-outline-icon/RectangleStackIcon props])
              :uri "/sessions"
              :free-feature? true}])

(def plugins-routes [{:name "review"
                      :label "Reviews"
                      :icon (fn [props]
                              [:> hero-outline-icon/InboxIcon props])
                      :uri "/reviews"
                      :free-feature? false
                      :navigate :reviews-plugin}])

(def plugins-management [{:name "access_control"
                          :label "Access Control"
                          :free-feature? false}
                         {:name "runbooks"
                          :label "Runbooks"
                          :free-feature? false}
                         {:name "audit"
                          :label "Audit"
                          :free-feature? true}
                         {:name "slack"
                          :label "Slack"
                          :free-feature? true}
                         {:name "webhooks"
                          :label "Webhooks"
                          :free-feature? true}
                         {:name "indexer"
                          :label "Indexer"
                          :free-feature? true}])
