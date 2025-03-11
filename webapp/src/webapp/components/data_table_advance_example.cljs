(ns webapp.components.data-table-advance-example
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text Heading Switch Button Badge]]
   ["lucide-react" :refer [Plus AlertCircle ChevronDown ChevronRight]]
   [reagent.core :as r]
   [webapp.components.data-table-advance :refer [data-table-advanced]]))

;; Example data
(def accounts-data
  [{:id "012345678901", :alias "nobelium-silver-wolf", :status "Active", :region "us-east-1", :type "Production"}
   {:id "098765432109", :alias "dysprosium-green-cat", :status "Active", :region "us-west-2", :type "Staging",
    :error {:message "User: arn:aws:iam::1234567890123:user/TestUser is not authorized to perform: s3:ListBucket on resource: arn:aws:s3:::example-bucket with an explicit deny",
            :code "AccessDenied",
            :type "Sender"}}
   {:id "086420135791", :alias "scandium-black-pelican", :status "Inactive", :region "eu-west-1", :type "Development"}
   {:id "135791086420", :alias "hassium-golden-turtle", :status "Active", :region "ap-southeast-1", :type "Production"}])

;; Resources data (hierarchical)
(def resources-data
  [{:id "Private-App-1a", :type :group, :subnet-cidr "10.0.1.0/24", :vpc-id "vpc-0a1b2c3d4e5f67890", :status "Active", :security-group true,
    :children [{:id "rds-mysql-prod-pri", :type :resource, :parent-id "Private-App-1a", :status "Active"}
               {:id "rds-mysql-prod-replica", :type :resource, :parent-id "Private-App-1a", :status "Active"}
               {:id "rds-mysql-staging", :type :resource, :parent-id "Private-App-1a", :status "Active",
                :error {:message "User: arn:aws:iam::1234567890123:user/TestUser is not authorized to perform: rds:DescribeDBInstances on resource: arn:aws:rds:us-east-1:1234567890123:db:rds-mysql-staging with an explicit deny",
                        :code "AccessDenied",
                        :type "Sender"}}]}
   {:id "Private-Data-1b", :type :group, :subnet-cidr "10.0.2.0/24", :vpc-id "vpc-1a2b3c4d5e6f78901", :status "Active", :security-group false,
    :children [{:id "rds-mysql-staging-1bsq", :type :resource, :parent-id "Private-Data-1b", :status "Inactive"}]}
   {:id "Public-Web-1c", :type :group, :subnet-cidr "10.0.3.0/24", :vpc-id "vpc-6a9b2c1d5e6f86320", :status "Active", :security-group false}
   {:id "Public-Web-2d", :type :group, :subnet-cidr "10.0.4.0/24", :vpc-id "vpc-0c1a8c1d5e6f72622", :status "Inactive", :security-group false}
   {:id "Private-Web-3f", :type :group, :subnet-cidr "10.0.5.0/24", :vpc-id "vpc-5a1b1c6d2e3f336707", :status "Active", :security-group false}])

;; Helper function to flatten hierarchical resources for display
(defn flatten-resources [data]
  (reduce
   (fn [acc group]
     (let [group-item (dissoc group :children)]
       (conj acc group-item)))
   []
   data))

;; Error renderer function
(defn render-error [row error-data]
  (when error-data
    [:div {:class "p-4 text-white"}
     [:pre {:class "whitespace-pre-wrap"}
      (js/JSON.stringify (clj->js error-data) nil 2)]]))

;; Status badge component
(defn status-badge [status]
  [:> Badge {:color (case status
                      "Active" "green"
                      "Inactive" "red"
                      "gray")
             :variant "soft"}
   status])

;; Advanced accounts table example
(defn accounts-table-advanced-example []
  (let [selected-ids (r/atom #{"012345678901" "098765432109"})
        expanded-rows (r/atom #{"098765432109"})
        update-counter (r/atom 0) ;; Contador para forçar atualizações
        columns [{:id :id, :header "Account ID", :width "25%"}
                 {:id :alias, :header "Alias", :width "20%"}
                 {:id :region, :header "Region", :width "15%"}
                 {:id :type, :header "Type", :width "15%"}
                 {:id :status, :header "Status", :width "15%",
                  :render (fn [value _] [status-badge value])}]]

    (fn []
      ;; Ignorar o valor do contador, mas usar para forçar atualizações
      @update-counter
      [:div
       [:> Heading {:size "5" :mb "4"} "Accounts Table Example (Advanced)"]

       [:> Flex {:justify "between" :align "center" :mb "4"}
        [:> Text {:size "3"} "Select accounts to manage"]
        [:> Button {:size "2"}
         [:> Plus {:size 16 :class "mr-2"}]
         "Add Account"]]

       [data-table-advanced
        {:columns columns
         :data accounts-data
         :selected-ids @selected-ids
         :on-select-row (fn [id selected?]
                          (swap! selected-ids (if selected? conj disj) id))
         :on-select-all (fn [select-all?]
                          (reset! selected-ids
                                  (if select-all?
                                    (into #{} (map :id accounts-data))
                                    #{})))
         :row-expandable? (fn [row] (boolean (:error row)))
         :row-expanded? (fn [row]
                          (let [is-expanded (contains? @expanded-rows (:id row))]
                            (js/console.log (str "Row " (:id row) " expanded? " is-expanded))
                            is-expanded))
         :on-toggle-expand (fn [id]
                             (js/console.log (str "Toggling expansion for: " id))
                             (swap! expanded-rows
                                    (fn [current]
                                      (if (contains? current id)
                                        (disj current id)
                                        (conj current id))))
                             ;; Incrementar o contador para forçar atualização
                             (swap! update-counter inc))
         :row-error (fn [row] (:error row))
         :error-indicator (fn [] [:> AlertCircle {:size 16 :class "text-red-500"}])
         :zebra-striping? true
         :compact? false
         :sticky-header? true}]])))

;; Error renderer function for resources
(defn render-error-resources [row error-data selected-ids expanded-rows update-counter]
  (if error-data
    ;; Render error
    [:div {:class "p-4 text-white"}
     [:pre {:class "whitespace-pre-wrap"}
      (js/JSON.stringify (clj->js error-data) nil 2)]]

    ;; Render nested resources if this is a group
    (when (= (:type row) :group)
      (let [original-group (first (filter #(= (:id %) (:id row)) resources-data))
            children (:children original-group)]
        (when (seq children)
          [:div {:class "bg-white p-2"}
           (for [child children]
             ^{:key (:id child)}
             [:div {:class (str "flex justify-between items-center p-2 border-t "
                                (when (:error child) "bg-red-50"))}
              [:div {:class "flex items-center"}
               [:input {:type "checkbox"
                        :checked (contains? @selected-ids (:id child))
                        :onChange #(let [selected? (.. % -target -checked)]
                                     (swap! selected-ids (if selected? conj disj) (:id child)))
                        :class "h-4 w-4 rounded border-gray-300 mr-3"}]
               [:span (:id child)]]
              [:div {:class "flex items-center"}
               [:span (:status child)]
               (when (:error child)
                 [:span {:class "ml-2"}
                  [:> AlertCircle {:size 16 :class "text-red-500"}]])
               (when (:error child)
                 [:button {:class "ml-3 focus:outline-none"
                           :on-click #(do
                                        (swap! expanded-rows
                                               (fn [current]
                                                 (if (contains? current (:id child))
                                                   (disj current (:id child))
                                                   (conj current (:id child)))))
                                        (swap! update-counter inc))}
                  (if (contains? @expanded-rows (:id child))
                    [:> ChevronDown {:size 16}]
                    [:> ChevronRight {:size 16}])])]])
           (for [child children
                 :when (and (:error child) (contains? @expanded-rows (:id child)))]
             ^{:key (str (:id child) "-error")}
             [:div {:class "p-4 bg-gray-900 text-white mt-1 rounded"}
              [:pre {:class "whitespace-pre-wrap"}
               (js/JSON.stringify (clj->js (:error child)) nil 2)]])])))))

;; Advanced resources table example with tree view
(defn resources-table-advanced-example []
  (let [selected-ids (r/atom #{"Private-App-1a" "rds-mysql-prod-pri" "rds-mysql-prod-replica"})
        expanded-rows (r/atom #{"Private-App-1a" "rds-mysql-staging" "Private-Data-1b" "Public-Web-1c"})
        update-counter (r/atom 0) ;; Contador para forçar atualizações
        columns [{:id :id, :header "Resources", :width "30%"}
                 {:id :subnet-cidr, :header "Subnet CIDR", :width "20%"}
                 {:id :vpc-id, :header "VPC ID", :width "25%"}
                 {:id :status, :header "Status", :width "15%",
                  :render (fn [value _] [status-badge value])}
                 {:id :security-group, :header "Security Group", :width "10%",
                  :render (fn [value _]
                            (when (not (nil? value))
                              [:> Switch {:checked value}]))}]]

    ;; Initialize some rows as expanded for demonstration
    (reset! expanded-rows #{"Private-App-1a" "rds-mysql-staging" "Private-Data-1b" "Public-Web-1c"})

    (fn []
      ;; Ignorar o valor do contador, mas usar para forçar atualizações
      @update-counter
      [:div
       [:> Heading {:size "5" :mb "4"} "Resources Table Example (Advanced)"]

       [:> Flex {:justify "between" :align "center" :mb "4"}
        [:> Text {:size "3"} "Select resources to manage"]
        [:> Button {:size "2"}
         [:> Plus {:size 16 :class "mr-2"}]
         "Add Resource"]]

       [data-table-advanced
        {:columns columns
         :data (flatten-resources resources-data)
         :original-data resources-data
         :selected-ids @selected-ids
         :on-select-row (fn [id selected?]
                          (let [item (first (filter #(= (:id %) id) (flatten-resources resources-data)))
                                is-group (= (:type item) :group)
                                original-group (when is-group (first (filter #(= (:id %) id) resources-data)))
                                children-ids (when is-group
                                               (map :id (:children original-group)))]
                            (swap! selected-ids
                                   (fn [current-ids]
                                     (let [updated-ids (if selected?
                                                         (conj current-ids id)
                                                         (disj current-ids id))]
                                       ;; If this is a group, also select/deselect all children
                                       (if is-group
                                         (if selected?
                                           (into updated-ids children-ids)
                                           (apply disj updated-ids children-ids))
                                         updated-ids))))))
         :on-select-all (fn [select-all?]
                          (reset! selected-ids
                                  (if select-all?
                                    (into #{} (map :id (flatten-resources resources-data)))
                                    #{})))
         :row-expandable? (fn [row]
                            (or (and (= (:type row) :group)
                                     ;; Verificar se o grupo tem filhos
                                     (let [original-group (first (filter #(= (:id %) (:id row)) resources-data))]
                                       (seq (:children original-group))))
                                (boolean (:error row))))
         :row-expanded? (fn [row]
                          (let [is-expanded (contains? @expanded-rows (:id row))]
                            (js/console.log (str "Row " (:id row) " expanded? " is-expanded))
                            is-expanded))
         :on-toggle-expand (fn [id]
                             (js/console.log (str "Toggling expansion for: " id))
                             (swap! expanded-rows
                                    (fn [current]
                                      (if (contains? current id)
                                        (disj current id)
                                        (conj current id))))
                             ;; Incrementar o contador para forçar atualização
                             (swap! update-counter inc))
         :row-error (fn [row] (:error row))
         :tree-data? true
         :parent-id-field "parent-id"
         :id-field "id"
         :zebra-striping? true
         :compact? false
         :sticky-header? true}]])))

;; Main example component that shows both tables
(defn data-table-advanced-examples []
  [:div {:class "space-y-8"}
   [accounts-table-advanced-example]
   [resources-table-advanced-example]])
