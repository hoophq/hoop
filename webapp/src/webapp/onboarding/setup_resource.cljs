(ns webapp.onboarding.setup-resource
  (:require
   ["@radix-ui/themes" :refer [Box Button Badge Callout]]
   ["lucide-react" :refer [AlertCircle]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.connections.views.setup.main :as setup]
   [webapp.components.data-table-advance :refer [data-table-advanced]]))

(defn main []
  [:<>
   [:> Box {:class "p-radix-5 bg-[--gray-1] text-right w-full"}
    [:> Button {:variant "ghost"
                :size "2"
                :color "gray"
                :on-click #(rf/dispatch [:auth->logout])}
     "Logout"]]
   [setup/main :onboarding]])

;; Status badge component
(defn status-badge [status]
  [:> Badge {:color (case status
                      ;; Positive states/available
                      "available" "green"

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
                      "inaccessible-encryption-credentials" "red"
                      "incompatible-network" "red"
                      "incompatible-option-group" "red"
                      "incompatible-parameters" "red"
                      "incompatible-restore" "red"
                      "restore-error" "red"

                      ;; Fallback for unknown status
                      "gray")
             :variant "soft"}
   status])

;; New implementation of AWS resources data table using proper patterns
(defn aws-resources-data-table []
  (let [resources @(rf/subscribe [:aws-connect/resources])
        rf-selected @(rf/subscribe [:aws-connect/selected-resources])
        rf-errors @(rf/subscribe [:aws-connect/resources-errors])
        resources-status @(rf/subscribe [:aws-connect/resources-status])
        api-error @(rf/subscribe [:aws-connect/resources-api-error])
        ;; Create local reagent atoms for state management
        selected-ids (r/atom (or rf-selected #{}))
        update-counter (r/atom 0)  ;; Counter to force re-renders

        ;; Define columns configuration
        columns [{:id "name"
                  :header "Resource"
                  :accessor :name
                  :width "25%"}
                 {:id "type"
                  :header "Type"
                  :accessor :engine
                  :width "20%"}
                 {:id "vpc-id"
                  :header "VPC ID"
                  :accessor :vpc-id
                  :width "25%"}
                 {:id "status"
                  :header "Status"
                  :width "15%"
                  :accessor :status
                  :render (fn [value _] [status-badge value])}]]

    ;; Watch for changes to selected-ids and dispatch to re-frame
    (add-watch selected-ids :selected-resources-sync
               (fn [_ _ _ new-value]
                 (rf/dispatch [:aws-connect/set-selected-resources new-value])))

    ;; Function component to render
    (fn []
      ;; Use update-counter to force re-renders on state changes
      @update-counter

      (if (= resources-status :error)
        ;; Show error message using a simpler pattern like in connections_list.cljs
        [:> Box {:class "p-5"}
         [:> Callout.Root {:color "red"}
          [:> Callout.Icon
           [:> AlertCircle {:size 16}]]
          [:> Callout.Text
           (:message api-error)]]]

        ;; Show resources table when API call succeeded
        [data-table-advanced
         {:columns columns
          :data resources
          :selected-ids @selected-ids
          :on-select-row (fn [id selected?]
                           (swap! selected-ids
                                  (if selected? conj disj) id)
                           (swap! update-counter inc))
          :on-select-all (fn [select-all?]
                           (reset! selected-ids
                                   (if select-all?
                                     (into #{} (map :id (filter #(not (contains? rf-errors (:id %))) resources)))
                                     #{}))
                           (swap! update-counter inc))
          :selectable? (fn [row]
                         (not (contains? rf-errors (:id row))))
          :zebra-striping true
          :compact false
          :sticky-header true}]))))
