(ns webapp.connections.views.setup.database
  (:require ["@radix-ui/themes" :refer [Box Flex Grid Heading RadioGroup Text]]
            ["lucide-react" :refer [Database]]
            [re-frame.core :as rf]
            [webapp.components.forms :as forms]
            [webapp.connections.views.setup.state :as state]))

(defn database-type-selector []
  [:> Box {:class "space-y-5"}
   [:> Text {:size "4" :weight "bold" :mb "2"} "Database type"]
   [:> RadioGroup.Root {:name "database-type"
                        :value @(rf/subscribe [:connection-setup/connection-subtype])
                        :on-value-change #(rf/dispatch [:connection-setup/select-subtype %])}
    [:> Flex {:direction "column" :gap "3"}
     (for [{:keys [id title]} state/database-types]
       ^{:key id}
       [:> RadioGroup.Item {:value id :class "p-4"}
        [:> Flex {:gap "3" :align "center"}
         [:> Database {:size 16}]
         title]])]]])

(defn database-credentials []
  (let [credentials @(rf/subscribe [:connection-setup/credentials])]
    [:> Box {:class "space-y-5"}
     [:> Text {:size "4" :weight "bold" :mb "2"} "Environment credentials"]

     [:> Grid {:columns "1" :gap "4"}
      [forms/input
       {:label "Host"
        :placeholder "e.g. localhost"
        :value (get credentials :host "")
        :on-change #(rf/dispatch [:connection-setup/update-credentials
                                  :host
                                  (-> % .-target .-value)])}]

      [forms/input
       {:label "User"
        :placeholder "e.g. username"
        :value (get credentials :user "")
        :on-change #(rf/dispatch [:connection-setup/update-credentials
                                  :user
                                  (-> % .-target .-value)])}]

      [forms/input
       {:label "Pass"
        :type "password"
        :placeholder "••••••••"
        :value (get credentials :pass "")
        :on-change #(rf/dispatch [:connection-setup/update-credentials
                                  :pass
                                  (-> % .-target .-value)])}]

      [forms/input
       {:label "Port"
        :placeholder "e.g. 5432"
        :value (get credentials :port "")
        :on-change #(rf/dispatch [:connection-setup/update-credentials
                                  :port
                                  (-> % .-target .-value)])}]

      [forms/input
       {:label "Database"
        :placeholder "e.g. mydb"
        :value (get credentials :database "")
        :on-change #(rf/dispatch [:connection-setup/update-credentials
                                  :database
                                  (-> % .-target .-value)])}]

      [forms/select
       {:label "SSL Mode (Optional)"
        :placeholder "Select one"
        :value (get credentials :ssl-mode "")
        :on-change #(rf/dispatch [:connection-setup/update-credentials
                                  :ssl-mode %])
        :options [{:value "disable" :text "Disable"}
                  {:value "require" :text "Require"}
                  {:value "verify-ca" :text "Verify CA"}
                  {:value "verify-full" :text "Verify Full"}]}]]]))

(defn main []
  (let [current-step @(rf/subscribe [:connection-setup/current-step])]
    [:> Box {:class "max-w-2xl mx-auto p-6"}
     [:> Box {:class "mb-8"}
      [:> Heading {:size "6" :mb "2"} "Setup database connection"]
      [:> Text {:size "3" :color "gray"}
       "Configure access to your database"]]

     (case current-step
       :database-type [database-type-selector]
       :credentials [database-credentials]
       [database-type-selector])]))
