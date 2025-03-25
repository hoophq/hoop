(ns webapp.components.data-table-simple-example
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text Heading Button Badge Select TextField]]
   ["lucide-react" :refer [Plus]]
   [reagent.core :as r]
   [webapp.components.data-table-simple :refer [data-table-simple]]
   [webapp.components.forms :as forms]))

;; Example data with hierarchical structure
(def resources-data
  [{:id "rds-mysql-prod"
    :name "MySQL Production"
    :engine "MySQL"
    :version "8.0.28"
    :status "Active"
    :children [{:id "rds-mysql-prod-pri"
                :name "MySQL Production Primary"
                :engine "MySQL"
                :version "8.0.28"
                :status "Active"
                :environment "production"
                :alias "mysql-primary"}
               {:id "rds-mysql-prod-read"
                :name "MySQL Production Read Replica"
                :engine "MySQL"
                :version "8.0.28"
                :status "Active"
                :environment "production"
                :alias "mysql-reader"}
               {:id "rds-mysql-prod-dr"
                :name "MySQL Production DR"
                :engine "MySQL"
                :version "8.0.28"
                :status "Inactive"
                :environment "dr"
                :alias "mysql-dr"}]}
   {:id "rds-mysql-staging"
    :name "MySQL Staging"
    :engine "MySQL"
    :version "8.0.28"
    :status "Active"
    :error {:message "User: arn:aws:iam::1234567890123:user/TestUser is not authorized to perform: rds:DescribeDBInstances on resource: arn:aws:rds:us-east-1:1234567890123:db:rds-mysql-staging"
            :code "AccessDenied"
            :type "Sender"}}
   {:id "rds-postgres-prod"
    :name "PostgreSQL Production"
    :engine "PostgreSQL"
    :version "14.2"
    :status "Active"
    :children [{:id "rds-postgres-prod-pri"
                :name "PostgreSQL Production Primary"
                :engine "PostgreSQL"
                :version "14.2"
                :status "Active"
                :environment "production"
                :alias "pg-primary"}
               {:id "rds-postgres-prod-read"
                :name "PostgreSQL Production Read Replica"
                :engine "PostgreSQL"
                :version "14.2"
                :status "Active"
                :environment "production"
                :alias "pg-reader"
                :error {:message "Failed to connect to database: connection timeout"
                        :code "ConnectionTimeout"
                        :type "Network"}}]}
   {:id "dynamodb-users"
    :name "DynamoDB Users Table"
    :engine "DynamoDB"
    :version "N/A"
    :status "Active"}
   {:id "aurora-serverless"
    :name "Aurora Serverless"
    :engine "Aurora PostgreSQL"
    :version "13.6"
    :status "Inactive"}])

;; Helper para obter todos os IDs (inclusive dos filhos)
(defn all-resource-ids []
  (into #{}
        (concat
         (map :id resources-data)
         (mapcat (fn [resource]
                   (when-let [children (:children resource)]
                     (map :id children)))
                 resources-data))))

;; Função para encontrar um recurso pelo ID
(defn find-resource-by-id [id]
  (or
   ;; Procura entre recursos de nível superior
   (first (filter #(= id (:id %)) resources-data))
   ;; Procura entre recursos filhos
   (first
    (for [resource resources-data
          :when (:children resource)
          child (:children resource)
          :when (= id (:id child))]
      child))))

;; Função para obter IDs dos filhos de um recurso
(defn get-child-ids [resource-id]
  (let [resource (find-resource-by-id resource-id)]
    (when-let [children (:children resource)]
      (map :id children))))

;; Função para obter o ID pai de um recurso filho
(defn get-parent-id [child-id]
  (some (fn [parent]
          (when-let [children (:children parent)]
            (when (some #(= child-id (:id %)) children)
              (:id parent))))
        resources-data))

;; Status badge component
(defn status-badge [status]
  [:> Badge {:color (case status
                      "Active" "green"
                      "Inactive" "red"
                      "gray")
             :variant "soft"}
   status])

;; Select de ambiente (componente para exemplo)
(defn environment-select [value row]
  (let [environment-state (r/atom value)]
    (fn []
      [forms/select
       {:selected @environment-state
        :not-margin-bottom? true
        :full-width? true
        :on-change #(reset! environment-state %)
        :options [{:value "production" :text "Production"}
                  {:value "staging" :text "Staging"}
                  {:value "dr" :text "Disaster Recovery"}
                  {:value "development" :text "Development"}]}])))

;; Input de alias (componente para exemplo)
(defn alias-input [value row]
  (let [alias-state (r/atom value)]
    (fn []
      [forms/input
       {:size "2"
        :variant "soft"
        :value @alias-state
        :on-change #(reset! alias-state (-> % .-target .-value))
        :style {:maxWidth "100%", :width "180px"}}])))

;; Main example component
(defn data-table-simple-example []
  (let [;; Estado de seleção e loading
        selected-ids-state (r/atom #{"rds-mysql-prod" "rds-postgres-prod-pri"})
        loading-state (r/atom false)

        ;; Funções de manipulação de seleção
        handle-select-row (fn [id selected?]
                            (let [parent-id (get-parent-id id)
                                  child-ids (get-child-ids id)]
                              (if selected?
                                ;; Ao selecionar
                                (swap! selected-ids-state
                                       (fn [current-selection]
                                         (cond-> current-selection
                                           ;; Adicionar o item atual
                                           true (conj id)

                                           ;; Adicionar todos os filhos se for um pai
                                           (seq child-ids) (into child-ids))))

                                ;; Ao desmarcar
                                (swap! selected-ids-state
                                       (fn [current-selection]
                                         (cond-> current-selection
                                           ;; Remover o item atual
                                           true (disj id)

                                           ;; Remover todos os filhos se for um pai
                                           (seq child-ids) (#(apply disj % child-ids))

                                           ;; Remover o pai se este for um filho (opcional - remove o pai se qualquer filho for desmarcado)
                                           parent-id (disj parent-id)))))))

        handle-select-all (fn [select-all?]
                            (reset! selected-ids-state
                                    (if select-all?
                                      (all-resource-ids)
                                      #{})))

        ;; Definição das colunas
        columns [{:id :name
                  :header "Database Name"
                  :width "30%"}
                 {:id :engine
                  :header "Engine"
                  :width "15%"}
                 {:id :version
                  :header "Version"
                  :width "10%"}
                 {:id :environment
                  :header "Environment"
                  :width "15%"
                  :render (fn [value row]
                            ;; Só renderizar o select se esse valor existir e se for um filho
                            (if (and value (get-parent-id (:id row)))
                              [environment-select value row]
                              "N/A"))}
                 {:id :alias
                  :header "Alias"
                  :width "15%"
                  :render (fn [value row]
                            ;; Só renderizar o input se esse valor existir e se for um filho
                            (if (and value (get-parent-id (:id row)))
                              [alias-input value row]
                              "N/A"))}
                 {:id :status
                  :header "Status"
                  :width "15%"
                  :render (fn [value _] [status-badge value])}]]

    ;; Função de renderização
    (fn []
      (let [selected-ids @selected-ids-state
            loading @loading-state]
        [:div
         [:> Heading {:size "5" :mb "4"} "Simplified Data Table Example"]

         [:> Flex {:justify "between" :align "center" :mb "4"}
          [:> Text {:size "3"}
           (str "Select resources to manage (" (count selected-ids) " selected)")]
          [:> Button {:size "2" :onClick #(swap! loading-state not)}
           [:> Plus {:size 16 :class "mr-2"}]
           (if loading "Stop Loading" "Simulate Loading")]]

         [data-table-simple
          {:columns columns
           :data resources-data
           :selected-ids selected-ids  ;; Passamos o valor desreferenciado aqui
           :loading? loading
           :on-select-row handle-select-row
           :on-select-all handle-select-all
           :empty-state "No database resources found"
           :sticky-header? true}]]))))
