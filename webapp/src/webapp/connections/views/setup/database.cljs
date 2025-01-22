(ns webapp.connections.views.setup.database
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Grid RadioGroup Text]]
   ["lucide-react" :refer [Database]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.connections.constants :refer [connection-configs-required]]
   [webapp.connections.views.setup.additional-configuration :as additional-configuration]
   [webapp.connections.views.setup.headers :as headers]))

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

(defn render-field [{:keys [key value required hidden placeholder]}]
  (let [base-props {:label key
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
  [:> Box {:class "space-y-5"}
   [:> Text {:size "4" :weight "bold"} "Database type"]
   [:> RadioGroup.Root {:name "database-type"
                        :value selected-type
                        :on-value-change #(rf/dispatch [:connection-setup/select-database-type %])}
    [:> Grid {:columns "1" :gap "3"}
     (for [{:keys [id title]} database-types]
       ^{:key id}
       [:> RadioGroup.Item {:value id :class "p-4"}
        [:> Flex {:gap "3" :align "center"}
         [:> Database {:size 16}]
         title]])]]

   (when selected-type
     [database-credentials selected-type])

   (when selected-type
     [:> Flex {:justify "end" :mt "6"}
      [:> Button {:size "3"
                  :on-click #(rf/dispatch [:connection-setup/update-step :additional-config])}
       "Next Configuration"]])])

(defn main []
  (let [selected-type @(rf/subscribe [:connection-setup/database-type])
        current-step @(rf/subscribe [:connection-setup/current-step])]
    [:> Box {:class "max-w-2xl mx-auto p-6"}
     [headers/setup-header]

     (case current-step
       :resource [resource-step selected-type]

       :additional-config
       [:<>
        [additional-configuration/main {:show-database-schema? true
                                        :selected-type selected-type}]
        [:> Flex {:justify "between" :mt "6"}
         [:> Button {:size "3"
                     :variant "soft"
                     :color "gray"
                     :on-click #(rf/dispatch [:connection-setup/update-step :resource])}
          "Back"]
         [:> Button {:size "3"
                     :on-click #(rf/dispatch [:connection-setup/submit])}
          "Confirm"]]]

            ;; Default retorna o mesmo componente do resource
       [resource-step selected-type])]))
