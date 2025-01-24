(ns webapp.connections.views.setup.database
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid Heading RadioGroup Text]]
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

(defn get-field-config [field-key]
  (case field-key
    "sslmode" {:options [{:value "disable" :text "Disable"}
                         {:value "require" :text "Require"}
                         {:value "verify-ca" :text "Verify CA"}
                         {:value "verify-full" :text "Verify Full"}]}
    "insecure" {:type "checkbox"}
    {}))

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

(defn resource-step [selected-type]
  [:> Box {:class "space-y-7"}
   [:> Box {:class "space-y-4"}
    [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"} "Database type"]
    [:> RadioGroup.Root {:name "database-type"
                         :value selected-type
                         :on-value-change #(rf/dispatch [:connection-setup/select-database-type %])}
     [:> Grid {:columns "1" :gap "3"}
      (for [{:keys [id title]} database-types]
        ^{:key id}
        [:> RadioGroup.Item {:value id}
         [:> Flex {:gap "4" :align "center"}
          title]])]]]

   (when selected-type
     [:<>
      [database-credentials selected-type]

      [agent-selector/main]])])

(defn main []
  (let [selected-type @(rf/subscribe [:connection-setup/database-type])
        current-step @(rf/subscribe [:connection-setup/current-step])
        ;;all-valid? @(rf/subscribe [:connection-setup/database-credentials-valid?])
        ]
    [page-wrapper/main
     {:children [:> Box {:class "max-w-[600px] mx-auto p-6 space-y-7"}
                 [headers/setup-header]

                 (case current-step
                   :resource [resource-step selected-type]
                   :additional-config [additional-configuration/main {:show-database-schema? true
                                                                      :selected-type selected-type}]
                   [resource-step selected-type])]

      :footer-props {:next-text (if (= current-step :additional-config)
                                  "Confirm"
                                  "Next: Configuration")
                     :next-disabled? (or (and (= current-step :resource)
                                              (not selected-type))
                                         #_(and (= current-step :resource)
                                                (not all-valid?)))
                     :on-next (if (= current-step :additional-config)
                                #(rf/dispatch [:connection-setup/submit])
                                #(rf/dispatch [:connection-setup/next-step]))}}]))
