(ns webapp.onboarding.setup-resource
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Callout]]
   ["lucide-react" :refer [AlertCircle]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.data-table-simple :refer [data-table-simple]]
   [webapp.connections.views.setup.main :as setup]))

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

        selected-ids (r/atom (or rf-selected #{}))
        expanded-rows (r/atom #{}) ;; Estado local para linhas expandidas
        update-counter (r/atom 0)

        ;; Add errors to resources
        resources-with-errors (map (fn [account]
                                     (let [children-with-errors (map (fn [resource]
                                                                       (if (contains? rf-errors (:id resource))
                                                                         (assoc resource :error {:message (get rf-errors (:id resource))
                                                                                                 :code "Error"
                                                                                                 :type "Access"})
                                                                         resource))
                                                                     (:children account))]
                                       (assoc account :children children-with-errors)))
                                   resources)

        columns [{:id :name
                  :header "Name"
                  :width "55%"}
                 {:id :id
                  :header "Account ID"
                  :width "35%"
                  :render (fn [value row]
                            (if (:account-type row)
                              (:id row)
                              ""))}
                 {:id :status
                  :header "Status"
                  :width "15%"
                  :render (fn [value _] [status-badge value])}]

        ;; Função para sincronizar apenas os IDs dos filhos com o re-frame
        sync-child-ids-only (fn [selected-set]
                              (let [all-child-ids (reduce (fn [acc account]
                                                            (let [child-ids (map :id (:children account))]
                                                              ;; Filtra para manter apenas IDs dos filhos, não dos pais
                                                              (apply conj acc
                                                                     (filter (fn [id]
                                                                               (some #(= id %) child-ids))
                                                                             selected-set))))
                                                          #{}
                                                          resources)]
                                (rf/dispatch [:aws-connect/set-selected-resources all-child-ids])))]

    ;; Observa mudanças no atom de seleção e sincroniza apenas IDs dos filhos
    (add-watch selected-ids :selected-resources-sync
               (fn [_ _ _ new-value]
                 (sync-child-ids-only new-value)))

    (fn []
      @update-counter

      (if (= resources-status :error)
        [:> Box {:class "p-5"}
         [:> Callout.Root {:color "red"}
          [:> Callout.Icon
           [:> AlertCircle {:size 16}]]
          [:> Callout.Text
           (:message api-error)]]]

        [data-table-simple
         {:columns columns
          :data resources-with-errors
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
                             ;; When selecting
                             (let [account (first (filter #(= id (:id %)) resources))
                                   child-ids (when account
                                               (map :id (:children account)))]
                               (if (seq child-ids)
                                 ;; Se for uma conta (pai):
                                 ;; 1. Seleciona a própria conta
                                 ;; 2. Seleciona todos os filhos
                                 ;; 3. Expande automaticamente para mostrar os filhos
                                 (do
                                   (swap! selected-ids #(conj % id))
                                   (swap! selected-ids #(apply conj % child-ids))
                                   (swap! expanded-rows #(conj % id)))
                                 ;; Se for um recurso (filho), apenas seleciona
                                 (swap! selected-ids conj id)))
                             ;; When deselecting
                             (let [account (first (filter #(= id (:id %)) resources))
                                   child-ids (when account
                                               (map :id (:children account)))]
                               (if (seq child-ids)
                                 ;; Se for uma conta (pai):
                                 ;; 1. Desmarca a própria conta
                                 ;; 2. Desmarca todos os filhos
                                 (do
                                   (swap! selected-ids #(disj % id))
                                   (swap! selected-ids #(apply disj % child-ids)))
                                 ;; Se for um recurso, desmarca apenas ele
                                 (swap! selected-ids disj id))))
                           (swap! update-counter inc))
          :on-select-all (fn [select-all?]
                           (if select-all?
                             ;; Select all rows (including parents)
                             (let [all-account-ids (map :id resources)
                                   all-resource-ids (mapcat (fn [account]
                                                              (map :id (:children account)))
                                                            resources)]
                               (reset! selected-ids (into #{} (concat all-account-ids all-resource-ids)))
                               ;; Expand all parent rows
                               (reset! expanded-rows (into #{} all-account-ids)))
                             ;; Deselect all
                             (do
                               (reset! selected-ids #{})
                               ;; Optionally collapse all rows when deselecting all
                               ;; (reset! expanded-rows #{})))
                               ))
                           (swap! update-counter inc))
          :selectable? (fn [row]
                         (and (not (contains? rf-errors (:id row)))
                              ;; Permitir seleção de contas (pais) e recursos (filhos)
                              true))
          :sticky-header? true
          :empty-state "No AWS resources found"}]))))
