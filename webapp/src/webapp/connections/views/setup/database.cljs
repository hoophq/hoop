(ns webapp.connections.views.setup.database
  (:require
   ["@radix-ui/themes" :refer [Box Flex Grid Heading RadioGroup Text]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.connections.constants :refer [connection-configs-required]]
   [webapp.connections.views.setup.additional-configuration :as additional-configuration]
   [webapp.connections.views.setup.agent-selector :as agent-selector]
   [webapp.connections.views.setup.headers :as headers]
   [webapp.connections.views.setup.page-wrapper :as page-wrapper]))

(def database-types
  [{:id "postgres" :title "PostgreSQL"}
   {:id "mysql" :title "MySQL"}
   {:id "mongodb" :title "MongoDB"}
   {:id "mssql" :title "Microsoft SQL"}
   {:id "oracledb" :title "Oracle DB"}])

(defn render-field [{:keys [key label value required hidden placeholder]}]
  (let [base-props {:label label
                    :placeholder (or placeholder (str "e.g. " key))
                    :value value
                    :required required
                    :type "password"
                    :hidden hidden
                    :on-change #(rf/dispatch [:connection-setup/update-database-credentials
                                              key
                                              (-> % .-target .-value)])}]
    [forms/input base-props]))

(defn database-credentials [selected-type]
  (let [configs (get connection-configs-required (keyword selected-type))
        credentials @(rf/subscribe [:connection-setup/database-credentials])]
    [:> Box {:class "space-y-5"}
     [:> Text {:size "4" :weight "bold" :mt "6"} "Environment credentials"]

     [:> Grid {:columns "1" :gap "4"}
      (for [field configs]
        ^{:key (:key field)}
        [render-field (assoc field
                             :value (get credentials (:key field) (:value field)))])]]))

(defn credentials-step [selected-subtype]
  [:> Box {:class "space-y-7"}
   [:> Box {:class "space-y-4"}
    [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"} "Database type"]
    [:> RadioGroup.Root {:name "database-type"
                         :value selected-subtype
                         :on-value-change #(rf/dispatch [:connection-setup/select-connection "database" %])}
     [:> Grid {:columns "1" :gap "3"}
      (for [{:keys [id title]} database-types]
        ^{:key id}
        [:> RadioGroup.Item {:value id}
         [:> Flex {:gap "4" :align "center"}
          title]])]]]

   (when selected-subtype
     [:<>
      [database-credentials selected-subtype]

      [agent-selector/main]])])

(defn main []
  (let [selected-subtype @(rf/subscribe [:connection-setup/connection-subtype])
        current-step @(rf/subscribe [:connection-setup/current-step])
        ;;all-valid? @(rf/subscribe [:connection-setup/database-credentials-valid?])
        ]

    [page-wrapper/main
     {:children [:> Box {:class "max-w-[600px] mx-auto p-6 space-y-7"}
                 [headers/setup-header]

                 (case current-step
                   :credentials [credentials-step selected-subtype]
                   :additional-config [additional-configuration/main {:show-database-schema? true
                                                                      :selected-type selected-subtype}]
                   nil)]

      :footer-props {:next-text (if (= current-step :additional-config)
                                  "Confirm"
                                  "Next: Configuration")
                     :next-disabled? (case current-step
                                       :credentials (not selected-subtype)
                                       false)
                     :on-next (case current-step
                                :additional-config #(rf/dispatch [:connection-setup/submit])
                                #(rf/dispatch [:connection-setup/next-step :additional-config]))}}]))
