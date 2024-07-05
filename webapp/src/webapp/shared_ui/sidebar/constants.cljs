(ns webapp.shared-ui.sidebar.constants
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]))

(def routes [{:name "Home"
              :label "Home"
              :icon (fn [props]
                      [:> hero-outline-icon/CodeBracketSquareIcon props])
              :uri "/client"
              :navigate :editor-plugin
              :free-feature? true
              :need-connection? false}
             {:name "Dashboard"
              :icon (fn [props]
                      [:> hero-outline-icon/RectangleGroupIcon props])
              :uri "/connections/details"
              :free-feature? true
              :need-connection? true}
             {:name "Sessions"
              :icon (fn [props]
                      [:> hero-outline-icon/RectangleStackIcon props])
              :uri "/sessions"
              :free-feature? true
              :need-connection? true}])

(def plugins-routes [{:name "runbooks"
                      :label "Runbooks"
                      :icon (fn [props]
                              [:> hero-outline-icon/BookOpenIcon props])
                      :uri "/runbooks"
                      :free-feature? false
                      :navigate :runbooks-plugin
                      :need-connection? true}
                     {:name "review"
                      :label "Reviews"
                      :icon (fn [props]
                              [:> hero-outline-icon/InboxIcon props])
                      :uri "/reviews"
                      :free-feature? false
                      :navigate :reviews-plugin
                      :need-connection? true}])

(def plugins-management [{:name "access_control"
                          :label "Access Control"
                          :free-feature? false}
                         {:name "dlp"
                          :label "AI Data Masking"
                          :free-feature? false}
                         {:name "review"
                          :label "Reviews"
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
