(ns webapp.audit-logs.table
  (:require
   ["@radix-ui/themes" :refer [Badge Box Flex Table Text]]
   ["lucide-react" :refer [ChevronDown ChevronRight Network BadgeCheck CircleAlert]]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [webapp.audit-logs.events]
   [webapp.audit-logs.subs]
   [webapp.components.infinite-scroll :refer [infinite-scroll]]))

(defn format-timestamp [timestamp]
  (when timestamp
    (let [date (js/Date. timestamp)
          day (.getDate date)
          month (inc (.getMonth date))
          year (.getFullYear date)
          hours (.getHours date)
          minutes (.getMinutes date)
          seconds (.getSeconds date)
          pad (fn [n] (if (< n 10) (str "0" n) (str n)))]
      (str (pad day) "/" (pad month) "/" year " " (pad hours) ":" (pad minutes) ":" (pad seconds)))))

(defn format-operation [action resource-type resource-name]
  (let [action-text (case action
                      "create" "Created"
                      "update" "Updated"
                      "delete" "Deleted"
                      "revoke" "Revoke"
                      (string/capitalize (or action "")))
        resource-text (cond
                        (not (string/blank? resource-name)) resource-name
                        (not (string/blank? resource-type)) (string/capitalize resource-type)
                        :else "Resource")]
    (str action-text " " resource-text)))

(defn outcome-badge [http-status]
  (if (and http-status (>= http-status 200) (< http-status 300))
    [:> Badge {:color "green" :variant "soft"}
     (str "Success (" http-status ")")]
    [:> Badge {:color "red" :variant "soft"}
     (str "Failure (" (or http-status "ERR") ")")]))

(defn expanded-content [log]
  [:> Box {:class "bg-[--gray-2]" :p "4"}
   [:> Box {:class "space-y-radix-5"}
    [:> Flex {:gap "2" :align "center"}
     [:> Network {:size 20}]
     [:> Text {:size "2" :class "text-[--gray-11]"}
      "IP Address:"]
     [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"}
      (or (:client_ip log) "N/A")]]

    [:> Flex {:gap "2" :align "center"}
     [:> BadgeCheck {:size 20}]
     [:> Text {:size "2" :class "text-[--gray-11]"}
      "Result:"]
     [outcome-badge (:http_status log)]]

    (when (and (or (nil? (:http_status log))
                   (>= (:http_status log) 400))
               (:error_message log)
               (not (string/blank? (:error_message log))))
      [:> Flex {:gap "2" :align "start"}
       [:> CircleAlert {:size 20}]
       [:> Text {:size "2" :class "text-[--gray-11]"}
        "Error Message:"]
       [:> Text {:size "2" :weight "medium" :class "text-[--red-11]"}
        (:error_message log)]])

    (when (and (:request_payload_redacted log)
               (not-empty (:request_payload_redacted log)))
      [:> Box {:class "space-y-radix-4"}
       [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
        "Raw Payload"]
       [:> Box {:class "bg-gray-900 text-white rounded-md p-3 overflow-x-auto"}
        [:pre {:class "text-xs"}
         (js/JSON.stringify (clj->js (:request_payload_redacted log)) nil 2)]]])]])

(defn table-row [log expanded-rows]
  (let [expanded? (contains? expanded-rows (:id log))]
    [:<>
     [:> Table.Row {:align "center"
                    :class "hover:bg-[--gray-3] transition-colors cursor-pointer"
                    :on-click #(rf/dispatch [:audit-logs/toggle-expand (:id log)])}
      [:> Table.Cell {:p "3"}
       [:> Text {:size "2" :class "text-[--gray-12]"}
        (format-timestamp (:created_at log))]]

      [:> Table.Cell {:p "3"}
       [:> Text {:size "2" :class "text-[--gray-12]"}
        (or (:actor_email log) (:actor_name log) "Unknown")]]

      [:> Table.Cell {:p "3"}
       [:> Text {:size "2" :class "text-[--gray-12]"}
        (format-operation (:action log) (:resource_type log) (:resource_name log))]]

      [:> Table.Cell {:p "2" :width "40px" :align "center" :justify "center"}
       [:button {:class "focus:outline-none text-[--gray-9] hover:text-[--gray-12] transition-colors"
                 :on-click (fn [e]
                             (.stopPropagation e)
                             (rf/dispatch [:audit-logs/toggle-expand (:id log)]))}
        (if expanded?
          [:> ChevronDown {:size 16}]
          [:> ChevronRight {:size 16}])]]]

     (when expanded?
       [:tr
        [:td {:colSpan 4 :class "p-0"}
         [expanded-content log]]])]))

(defn main []
  (let [audit-logs-state (rf/subscribe [:audit-logs/data])
        expanded-rows (rf/subscribe [:audit-logs/expanded-rows])
        pagination (rf/subscribe [:audit-logs/pagination])]
    (fn []
      (let [data (:data @audit-logs-state)
            status (:status @audit-logs-state)
            has-more? (:has-more? @pagination)
            loading? (= status :loading)]
        [:> Box {:class "space-y-radix-4"}
         [:> Table.Root
          [:> Table.Header
           [:> Table.Row
            [:> Table.ColumnHeaderCell {:p "3"}
             [:> Text {:size "2" :weight "medium"} "Timestamp"]]
            [:> Table.ColumnHeaderCell {:p "3"}
             [:> Text {:size "2" :weight "medium"} "User"]]
            [:> Table.ColumnHeaderCell {:p "3"}
             [:> Text {:size "2" :weight "medium"} "Operation"]]
            [:> Table.ColumnHeaderCell {:p "2" :width "40px"}]]]

          [:> Table.Body
           (if (and (= status :loading) (empty? data))
             [:tr
              [:td {:colSpan 4 :class "p-4 text-center"}
               [:div {:class "flex justify-center items-center p-4"}
                "Loading..."]]]

             (if (empty? data)
               [:tr
                [:td {:colSpan 4 :class "p-4 text-center text-gray-500"}
                 "No audit logs found"]]

               [infinite-scroll
                {:on-load-more #(rf/dispatch [:audit-logs/load-more])
                 :has-more? has-more?
                 :loading? loading?}
                (doall
                 (for [log data]
                   ^{:key (:id log)}
                   [table-row log @expanded-rows]))]))]]

         (when (and loading? (seq data))
           [:> Flex {:justify "center" :align "center" :class "mt-radix-4"}
            [:> Text {:size "2" :class "text-[--gray-11]"}
             "Loading more..."]])]))))
