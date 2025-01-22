(ns webapp.connections.views.setup.additional-configuration
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading Switch Text]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.connections.dlp-info-types :as dlp-info-types]
   [webapp.connections.helpers :as helpers]))

(defn toggle-section [{:keys [title description checked on-change]}]
  [:> Flex {:align "center" :gap "5"}
   [:> Switch {:checked checked
               :size "3"
               :onCheckedChange on-change}]
   [:> Box
    [:> Heading {:as "h4" :size "3" :weight "medium" :class "text-[--gray-12]"} title]
    [:> Text {:as "p" :size "2" :class "text-[--gray-11]"} description]]])

(defn main [{:keys [show-database-schema? selected-type]}]
  (let [user-groups (rf/subscribe [:user-groups])
        review? (rf/subscribe [:connection-setup/review])
        review-groups (rf/subscribe [:connection-setup/review-groups])
        data-masking? (rf/subscribe [:connection-setup/data-masking])
        data-masking-types (rf/subscribe [:connection-setup/data-masking-types])
        database-schema? (rf/subscribe [:connection-setup/database-schema])
        access-modes (rf/subscribe [:connection-setup/access-modes])
        connection-name (rf/subscribe [:connection-setup/name])
        tags (rf/subscribe [:connection-setup/tags])
        tags-input (rf/subscribe [:connection-setup/tags-input])]

    (rf/dispatch [:users->get-user-groups])
    (fn []
      [:> Box {:class "space-y-7"}
       [:> Box
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]" :mb "5"}
         "Connection information"]

        [forms/input {:placeholder (str (when selected-type
                                          (str selected-type "-"))
                                        (helpers/random-connection-name))
                      :label "Name"
                      :required true
                      :defaultValue @connection-name
                      :on-change #(rf/dispatch [:connection-setup/set-name
                                                (-> % .-target .-value)])}]

        [multi-select/text-input
         {:value @tags
          :input-value @tags-input
          :on-change #(rf/dispatch [:connection-setup/set-tags %])
          :on-input-change #(rf/dispatch [:connection-setup/set-tags-input %])
          :label "Tags"
          :id "tags-multi-select-text-input"
          :name "tags-multi-select-text-input"}]]


       [:> Box
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]" :mb "5"}
         "Additional Configuration"]
        [:> Box {:class "space-y-7"}
                                                                 ;; Reviews
         [:> Box {:class "space-y-2"}
          [toggle-section
           {:title "Reviews"
            :description "Require approval prior to connection execution. Enable Just-in-Time access for 30-minute sessions or Command reviews for individual query approvals."
            :checked @review?
            :on-change #(rf/dispatch [:connection-setup/toggle-review])}]

          (when @review?
            [:> Box {:mt "4"}
             [multi-select/main
              {:options (helpers/array->select-options @user-groups)
               :id "approval-groups-input"
               :name "approval-groups-input"
               :required? @review?
               :default-value @review-groups
               :on-change #(rf/dispatch [:connection-setup/set-review-groups (js->clj %)])}]])]

                                                                 ;; AI Data Masking
         [:> Box {:class "space-y-2"}
          [toggle-section
           {:title "AI Data Masking"
            :description "Provide an additional layer of security by ensuring sensitive data is masked in query results with AI-powered data masking."
            :checked @data-masking?
            :on-change #(rf/dispatch [:connection-setup/toggle-data-masking])}]

          (when @data-masking?
            [:> Box {:mt "4"}
             [multi-select/main
              {:options (helpers/array->select-options dlp-info-types/options)
               :id "data-masking-groups-input"
               :name "data-masking-groups-input"
               :required? @data-masking?
               :default-value @data-masking-types
               :on-change #(rf/dispatch [:connection-setup/set-data-masking-types (js->clj %)])}]])]

                                                                 ;; Database schema (condicionalmente renderizado)
         (when show-database-schema?
           [:> Box {:class "space-y-2"}
            [toggle-section
             {:title "Database schema"
              :description "Show database schema in the Editor section."
              :checked @database-schema?
              :on-change #(rf/dispatch [:connection-setup/toggle-database-schema])}]])]]

       [:> Box
        [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
         "Access modes"]
        [:> Text {:as "p" :size "3" :class "text-[--gray-11]" :mb "5"}
         "Setup how users interact with this connection."]

        [:> Box {:class "space-y-7"}                                                       ;; Runbooks
         [toggle-section
          {:title "Runbooks"
           :description "Create templates to automate tasks in your organization."
           :checked (get @access-modes :runbooks true)
           :on-change #(rf/dispatch [:connection-setup/toggle-access-mode :runbooks])}]

                                                                   ;; Native
         [toggle-section
          {:title "Native"
           :description "Access from your client of preference using hoop.dev to channel connections using our Desktop App or our Command Line Interface."
           :checked (get @access-modes :native true)
           :on-change #(rf/dispatch [:connection-setup/toggle-access-mode :native])}]

                                                                   ;; Web and one-offs
         [toggle-section
          {:title "Web and one-offs"
           :description "Use hoop.dev's developer portal or our CLI's One-Offs commands directly in your terminal."
           :checked (get @access-modes :web true)
           :on-change #(rf/dispatch [:connection-setup/toggle-access-mode :web])}]]]])))
