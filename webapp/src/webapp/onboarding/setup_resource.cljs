(ns webapp.onboarding.setup-resource
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Flex Callout]]
   ["lucide-react" :refer [AlertCircle]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.data-table-simple :refer [data-table-simple]]
   [webapp.components.forms :as forms]
   [webapp.resources.setup.main :as setup]))

(defn main []
  [:<>
   [:> Box {:class "p-radix-5 bg-[--gray-1] text-right w-full"}
    [:> Button {:variant "ghost"
                :size "2"
                :color "gray"
                :on-click #(rf/dispatch [:auth->logout])}
     "Logout"]]
   [setup/main :onboarding]])

(defn status-badge [status]
  [:> Badge {:color (case status
                      ;; Positive states/available
                      "available" "green"
                      "ACTIVE" "green"

                      ;; State processing/transition
                      "backing-up" "blue"
                      "configuring-enhanced-monitoring" "blue"
                      "configuring-iam-database-auth" "blue"
                      "configuring-log-exports" "blue"
                      "converting-to-vpc" "blue"
                      "creating" "blue"
                      "maintenance" "blue"
                      "modifying" "blue"
                      "moving-to-vpc" "blue"
                      "rebooting" "blue"
                      "renaming" "blue"
                      "resetting-master-credentials" "blue"
                      "starting" "blue"
                      "storage-optimization" "blue"
                      "upgrading" "blue"

                      ;; Alert states
                      "stopped" "yellow"
                      "stopping" "yellow"
                      "storage-full" "orange"

                      ;; Negative states/failures
                      "deleting" "red"
                      "failed" "red"
                      "Inactive" "red"
                      "SUSPENDED" "red"
                      "inaccessible-encryption-credentials" "red"
                      "incompatible-network" "red"
                      "incompatible-option-group" "red"
                      "incompatible-parameters" "red"
                      "incompatible-restore" "red"
                      "restore-error" "red"

                      ;; Fallback for unknown status
                      "gray")
             :variant "soft"}
   (cs/lower-case status)])

(defn aws-resources-data-table []
  (let [resources @(rf/subscribe [:aws-connect/resources])
        rf-selected @(rf/subscribe [:aws-connect/selected-resources])
        rf-errors @(rf/subscribe [:aws-connect/resources-errors])
        resources-status @(rf/subscribe [:aws-connect/resources-status])
        api-error @(rf/subscribe [:aws-connect/resources-api-error])
        security-groups (rf/subscribe [:aws-connect/security-groups])

        selected-ids (r/atom (or rf-selected #{}))
        expanded-rows (r/atom #{})
        update-counter (r/atom 0)
        security-groups-atom (r/atom @security-groups)

        formatted-api-error (when (and (= resources-status :error) api-error)
                              {:message (or (:message api-error) "Unknown error occurred")
                               :code (or (:code api-error) "Error")
                               :type (or (:type api-error) "Failed")})

        apply-sg-to-resource (fn [resource-id current-sg]
                               (let [account (first (filter #(= (:id %) resource-id) resources))
                                     child-resources (:children account)]
                                 (doseq [resource child-resources]
                                   (rf/dispatch [:aws-connect/set-security-group (:id resource) current-sg]))))

        columns [{:id :name
                  :header "Name"
                  :width "30%"}
                 {:id :id
                  :header "Account ID"
                  :width "15%"
                  :render (fn [_value row]
                            (if (:account-type row)
                              (:id row)
                              ""))}
                 {:id :status
                  :header "Status"
                  :width "10%"
                  :render (fn [value _] [status-badge value])}
                 {:id :security-group
                  :header "Security Group"
                  :width "25%"
                  :render (fn [_ row]
                            (if (:account-type row)
                              ;; Parent row - don't show input
                              ""
                              ;; Child row - show the input
                              (let [resource-id (:id row)
                                    account-id (:account-id row)
                                    current-sg (get @security-groups resource-id "")]
                                [:> Flex {:align "center" :gap "2"}
                                 [forms/input
                                  {:placeholder "e.g. 10.10.10.10/32"
                                   :value current-sg
                                   :not-margin-bottom? true
                                   :on-change #(do
                                                 (swap! security-groups-atom assoc resource-id (-> % .-target .-value))
                                                 (rf/dispatch [:aws-connect/set-security-group
                                                               resource-id
                                                               (-> % .-target .-value)]))}]
                                 [:> Button {:size "1"
                                             :variant "soft"
                                             :disabled (empty? current-sg)
                                             :on-click #(apply-sg-to-resource account-id current-sg)}
                                  "Apply to all"]])))}]

        sync-child-ids-only
        (fn [selected-set]
          (let [all-child-ids (reduce (fn [acc account]
                                        (let [child-ids (map :id (:children account))]
                                          (apply conj acc
                                                 (filter (fn [id]
                                                           (some #(= id %) child-ids))
                                                         selected-set))))
                                      #{}
                                      resources)]
            (rf/dispatch [:aws-connect/set-selected-resources all-child-ids])))]

    (add-watch selected-ids :selected-resources-sync
               (fn [_ _ _ new-value]
                 (sync-child-ids-only new-value)))

    (fn []
      @update-counter

      (when (not= @security-groups-atom @security-groups)
        (reset! security-groups-atom @security-groups))

      (if (= resources-status :error)
        [:> Box {:class "p-5"}
         [:> Callout.Root {:color "red"}
          [:> Callout.Icon
           [:> AlertCircle {:size 16}]]
          [:> Callout.Text
           (if formatted-api-error
             (:message formatted-api-error)
             "Failed to load AWS resources. Please check your credentials and try again.")]]]

        [data-table-simple
         {:columns columns
          :data resources
          :selected-ids @selected-ids
          :expanded-rows @expanded-rows
          :on-toggle-expand (fn [id]
                              (swap! expanded-rows
                                     #(if (contains? % id)
                                        (disj % id)
                                        (conj % id)))
                              (swap! update-counter inc))
          :on-select-row (fn [id selected?]
                           (if selected?
                             (let [account (first (filter #(= id (:id %)) resources))
                                   child-ids (when account
                                               (map :id (:children account)))]
                               (if (seq child-ids)
                                 (do
                                   (swap! selected-ids #(conj % id))
                                   (swap! selected-ids #(apply conj % child-ids))
                                   (swap! expanded-rows #(conj % id)))
                                 (swap! selected-ids conj id)))
                             (let [account (first (filter #(= id (:id %)) resources))
                                   child-ids (when account
                                               (map :id (:children account)))]
                               (if (seq child-ids)
                                 (do
                                   (swap! selected-ids #(disj % id))
                                   (swap! selected-ids #(apply disj % child-ids)))
                                 (swap! selected-ids disj id))))
                           (swap! update-counter inc))
          :on-select-all (fn [select-all?]
                           (if select-all?
                             (let [all-account-ids (map :id resources)
                                   all-resource-ids (mapcat (fn [account]
                                                              (map :id (:children account)))
                                                            resources)]
                               (reset! selected-ids (into #{} (concat all-account-ids all-resource-ids)))
                               (reset! expanded-rows (into #{} all-account-ids)))
                             (do
                               (reset! selected-ids #{})
                               (reset! expanded-rows #{})))
                           (swap! update-counter inc))
          :selectable? (fn [row]
                         (and (not (contains? rf-errors (:id row)))
                              (not (:error row))))
          :sticky-header? true
          :empty-state "No AWS resources found"}]))))
