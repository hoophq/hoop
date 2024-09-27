(ns webapp.shared-ui.sidebar.constants
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            [webapp.routes :as routes]))

(def routes [{:name "Home"
              :label "Home"
              :icon (fn [props]
                      [:> hero-outline-icon/CodeBracketSquareIcon props])
              :uri (routes/url-for :editor-plugin)
              :navigate :editor-plugin
              :free-feature? true
              :admin-only? false}
             {:name "Dashboard"
              :label "Dashboard"
              :icon (fn [props]
                      [:> hero-outline-icon/RectangleGroupIcon props])
              :uri (routes/url-for :dashboard)
              :navigate :dashboard
              :free-feature? false
              :admin-only? true}
             {:name "Sessions"
              :icon (fn [props]
                      [:> hero-outline-icon/RectangleStackIcon props])
              :uri (routes/url-for :sessions)
              :free-feature? true
              :admin-only? false}])

(def plugins-routes [{:name "review"
                      :label "Reviews"
                      :icon (fn [props]
                              [:> hero-outline-icon/InboxIcon props])
                      :uri (routes/url-for :reviews-plugin)
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
