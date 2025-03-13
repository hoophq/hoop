(ns webapp.integrations.aws-connect
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Card Flex Heading Spinner Text]]
   ["lucide-react" :refer [Cloud RefreshCw]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.data-table-simple :refer [data-table-simple]]
   [webapp.events.jobs]
   [webapp.integrations.events]))

(defn transform-job-data [job]
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
                                  (let [item-status (:status item)
                                        item-message (or (:message item) "")
                                        has-error? (= item-status "failed")]
                                    {:id (str (:id job) "-" (or (:user_role item) idx))
                                     :type :resource
                                     :parent-id (:id job)
                                     :display_name (str display-name "-" (last (cs/split (:user_role item) "_")))
                                     :role (or (:user_role item) "unknown")
                                     :status item-status
                                     :message item-message
                                     :completed_at (:completed_at item)
                                     :error (when has-error?
                                              {:message item-message
                                               :code "Error"
                                               :type "Failed"})}))
                                result))]
    (let [has-error? (= phase "failed")
          job-data {:id (:id job)
                    :type :group
                    :display_name display-name
                    :job_type db-engine
                    :status phase
                    :created_at (:created_at job)
                    :completed_at (:completed_at job)
                    :message (str (when db-name (str db-name ": ")) message)
                    :spec (:spec job)
                    :children children}]
      ;; Adicionar erro se o status for failed
      (if has-error?
        (assoc job-data :error {:message message
                                :code "Error"
                                :type "Failed"})
        job-data))))

(defn transform-jobs-to-hierarchical [jobs]
  (let [transformed (map transform-job-data jobs)
        sorted (sort-by :created_at #(compare %2 %1) transformed)]
    sorted))

(rf/reg-sub
 :integrations/formatted-aws-connect-jobs
 :<- [:jobs/aws-connect-jobs]
 (fn [jobs _]
   (transform-jobs-to-hierarchical jobs)))

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

(defn jobs-table-component []
  (let [update-counter (r/atom 0)]
    (fn []
      (let [jobs @(rf/subscribe [:integrations/formatted-aws-connect-jobs])
            running? @(rf/subscribe [:jobs/aws-connect-running?])]

        (swap! update-counter inc)

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

         [data-table-simple
          {:columns [{:id :display_name
                      :header "Resource"
                      :width "35%"}
                     {:id :status
                      :header "Status"
                      :width "15%"
                      :render (fn [value _] [job-status-badge value])}
                     {:id :created_at
                      :header "Created At"
                      :width "25%"
                      :render (fn [value _] (format-date value))}
                     {:id :completed_at
                      :header "Completed At"
                      :width "25%"
                      :render (fn [value _] (format-date value))}]
           :data jobs
           :key-fn :id
           :empty-state "No database discovery processes found. Start a new AWS connection to automatically discover and configure your database resources."
           :sticky-header? true}]]))))

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

(defn main []
  (r/create-class
   {:component-did-mount
    (fn []
      (rf/dispatch [:jobs/fetch-aws-connect-jobs]))

    :component-will-unmount
    (fn []
      (rf/dispatch [:jobs/stop-aws-connect-polling]))

    :reagent-render
    (fn []
      [:> Box {:class "space-y-7"}
       [aws-connect-button]
       [jobs-table-component]])}))

(defn panel []
  [main])
