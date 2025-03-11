(ns webapp.integrations.aws-connect
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Card Container Flex Heading
                               Spinner Table Text]]
   ["lucide-react" :refer [AlertCircle Cloud RefreshCw]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.data-table-advance :refer [data-table-advanced]]
   [webapp.events.jobs]
   [webapp.integrations.events]))

;; -------------------------
;; Formatação dos dados de jobs
;; -------------------------

(defn transform-job-data [job]
  "Transforma um processo de descoberta da API para o formato esperado pela tabela hierárquica"
  (let [phase (get-in job [:status :phase])
        message (get-in job [:status :message])
        db-arn (get-in job [:spec :db_arn])
        db-name (get-in job [:spec :db_name])
        db-engine (get-in job [:spec :db_engine])
        result (get-in job [:status :result])

        display-name (last (cs/split db-arn ":"))
        ;; Transformar os resultados em filhos
        children (when (seq result)
                   (map-indexed (fn [idx item]
                                  {:id (str (:id job) "-" (or (:user_role item) idx))
                                   :type :resource
                                   :parent-id (:id job)
                                   :display_name (str display-name "-" (last (cs/split (:user_role item) "_")))
                                   :role (or (:user_role item) "unknown")
                                   :status (:status item)
                                   :message (or (:message item) "")
                                   :completed_at (:completed_at item)})
                                result))]
    {:id (:id job)
     :type :group
     :display_name display-name
     :job_type db-engine
     :status phase
     :created_at (:created_at job)
     :completed_at (:completed_at job)
     :message (str (when db-name (str db-name ": ")) message)
     :spec (:spec job)
     :children children}))

(defn transform-jobs-to-hierarchical [jobs]
  (let [transformed (map transform-job-data jobs)
        ;; Ordenar por created_at do mais novo para o mais antigo
        sorted (sort-by :created_at #(compare %2 %1) transformed)]
    sorted))

(rf/reg-sub
 :integrations/formatted-aws-connect-jobs
 :<- [:jobs/aws-connect-jobs]
 (fn [jobs _]
   (transform-jobs-to-hierarchical jobs)))

(rf/reg-sub
 :integrations/flattened-aws-connect-jobs
 :<- [:integrations/formatted-aws-connect-jobs]
 (fn [jobs _]
   (reduce
    (fn [acc group]
      (let [group-item (dissoc group :children)]
        (conj acc group-item)))
    []
    jobs)))

;; -------------------------
;; Components
;; -------------------------

(defn job-status-badge [status]
  (case status
    "running" [:> Badge {:color "orange" :size "1"} "In Progress"]
    "completed" [:> Badge {:color "green" :size "1"} "Completed"]
    "failed" [:> Badge {:color "red" :size "1"} "Failed"]
    "error" [:> Badge {:color "red" :size "1"} "Error"]
    [:> Badge {:color "gray" :size "1"} (str status)]))

(defn format-date [date-str]
  (when date-str
    (try
      (let [date (js/Date. date-str)]
        (.toLocaleString date))
      (catch :default _
        date-str))))

(defn format-job-details [row]
  [:> Box {:class "p-4 space-y-3"}
   ;; Cabeçalho com informações gerais do processo
   [:> Flex {:direction "column" :gap "2"}
    [:> Heading {:as "h4" :size "3"} (str (:job_type row) " - " (:id row))]
    [:> Text {:as "p" :size "2" :color "gray"}
     (str "Started: " (format-date (:created_at row))
          (when (:completed_at row)
            (str " | Completed: " (format-date (:completed_at row)))))]
    [:> Text {:as "p" :size "2"} (:message row)]]

   (when (:spec row)
     [:> Card {:size "1" :class "mt-2"}
      [:> Flex {:direction "column" :gap "1" :class "p-3"}
       [:> Heading {:as "h5" :size "2"} "Discovery Configuration"]
       [:> Box {:class "text-xs space-y-1"}
        (for [[key value] (:spec row)]
          ^{:key (str "spec-" key)}
          [:> Flex {:justify "between" :class "border-b border-gray-100 py-1 last:border-0"}
           [:> Text {:color "gray"} (name key)]
           [:> Text (str value)]])]]])

   ;; Resultados de subtarefas
   (when (seq (:result row))
     [:> Card {:size "1" :class "mt-2"}
      [:> Flex {:direction "column" :gap "1" :class "p-3"}
       [:> Heading {:as "h5" :size "2"} "Connection Results"]
       [:> Table
        [:thead
         [:tr
          [:th {:style {:width "30%"}} [:> Text {:size "1" :weight "medium"} "Database Role"]]
          [:th {:style {:width "20%"}} [:> Text {:size "1" :weight "medium"} "Status"]]
          [:th {:style {:width "50%"}} [:> Text {:size "1" :weight "medium"} "Details"]]]]
        [:tbody
         (for [item (:result row)]
           ^{:key (str "result-" (or (:user_role item) (:id item)))}
           [:tr
            [:td [:> Text {:size "1"} (or (:user_role item) "—")]]
            [:td [job-status-badge (or (:status item) "unknown")]]
            [:td [:> Text {:size "1"}
                  (or (:message item)
                      (when (:completed_at item)
                        (str "Completed at " (format-date (:completed_at item))))
                      "No details")]]])]]]])])

(defn jobs-table-component []
  (let [expanded-rows (r/atom #{})
        update-counter (r/atom 0)]
    (fn []
      (let [jobs @(rf/subscribe [:integrations/formatted-aws-connect-jobs])
            flattened-jobs @(rf/subscribe [:integrations/flattened-aws-connect-jobs])
            running? @(rf/subscribe [:jobs/aws-connect-running?])]

        ;; Ignorar o valor do contador, mas usar para forçar atualizações
        @update-counter

        [:> Box {:class "w-full"}
         [:> Flex {:justify "between" :align "center" :width "100%" :class "px-1 mb-2"}
          [:> Heading {:as "h3" :size "4"} "Connection Creation Processes"]
          [:> Flex {:align "center" :gap "2"}
           (when running?
             [:> Flex {:align "center" :gap "1" :class "text-primary-11"}
              [:> Spinner {:size "1"}]
              [:> Text {:size "2"} "Connection creation in progress"]])
           [:> Button {:size "1"
                       :variant "soft"
                       :on-click #(rf/dispatch [:jobs/fetch-aws-connect-jobs])}
            [:> RefreshCw {:size 14}]
            "Refresh"]]]

         [data-table-advanced
          {:columns [{:id :id, :header "Resource", :width "35%",
                      :accessor (fn [row] (or (:display_name row) (:id row)))}
                     {:id :status, :header "Status", :width "15%",
                      :render (fn [value _] [job-status-badge value])}
                     {:id :created_at, :header "Created At", :width "25%"
                      :render (fn [value _] (format-date value))}
                     {:id :completed_at, :header "Completed At", :width "25%",
                      :render (fn [value _] (format-date value))}]
           :data flattened-jobs
           :original-data jobs
           :key-fn :id
           :row-expandable? (fn [row]
                              (let [original-group (first (filter #(= (:id %) (:id row)) jobs))]
                                (and (= (:type row) :group)
                                     (seq (:children original-group)))))
           :row-expanded? (fn [row]
                            (contains? @expanded-rows (:id row)))
           :on-toggle-expand (fn [id]
                               (swap! expanded-rows
                                      (fn [current]
                                        (if (contains? current id)
                                          (disj current id)
                                          (conj current id))))
                              ;; Incrementar o contador para forçar atualização
                               (swap! update-counter inc))
           :row-error (fn [row]
                        (when (= (:status row) "failed")
                          {:message (str "Discovery failed: " (or (:message row) "Unknown error"))
                           :details [format-job-details row]}))
           :error-indicator (fn [] [:> AlertCircle {:size 16 :class "text-red-500"}])
           :empty-state "No database discovery processes found. Start a new AWS connection to automatically discover and configure your database resources."
           :tree-data? true
           :parent-id-field "parent-id"}]]))))

(defn aws-connect-button []
  [:> Card {:size "2" :class "w-full mb-6"}
   [:> Flex {:direction "column" :gap "4" :align "start" :justify "center" :class "p-6"}
    [:> Flex {:align "center" :gap "2"}
     [:> Cloud {:size 24 :className "text-blue-500"}]
     [:> Heading {:as "h2" :size "5"} "Automatic AWS Database Discovery"]]
    [:> Text {:as "p" :size "2"}
     "Connect to your AWS environment to automatically discover and configure your database resources. This automated process will scan your AWS account for database instances and create connections in Hoop, saving you time and ensuring proper configuration."]
    [:> Button {:size "3"
                :class "mt-2"
                :on-click (fn []
                            (rf/dispatch [:navigate :integrations-aws-connect-setup]))}
     "Discover AWS Databases"]]])

;; -------------------------
;; Main component
;; -------------------------

(defn main []
  (r/create-class
   {:component-did-mount
    (fn []
      ;; Iniciar o polling de jobs quando o componente é montado
      (rf/dispatch [:jobs/fetch-aws-connect-jobs]))

    :component-will-unmount
    (fn []
      ;; Parar o polling quando sair da página
      (rf/dispatch [:jobs/stop-aws-connect-polling]))

    :reagent-render
    (fn []
      [:> Flex {:direction "column" :align "start" :gap "6" :width "100%" :height "100%"}
       [aws-connect-button]
       [jobs-table-component]])}))

(defn panel []
  [main])
