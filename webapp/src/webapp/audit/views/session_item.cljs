(ns webapp.audit.views.session-item
  (:require
   [clojure.string :as string]
   [re-frame.core :as rf]
   ["@radix-ui/themes" :refer [Badge Box Button Flex Grid Text Tooltip]]
   ["lucide-react" :refer [CircleCheckBig Clock2 OctagonX Workflow]]
   [webapp.formatters :as formatters]
   [webapp.components.user-icon :as user-icon]
   [webapp.components.icon :as icon]
   [webapp.audit.views.session-details :as session-details]))

(defmulti ^:private access-request-badge identity)
(defmethod ^:private access-request-badge "APPROVED" [_]
  [:> Badge {:color "green" :size "2"}
   [:> Flex {:gap "1" :align "center"}
    [:> CircleCheckBig {:size 14}]
    "Approved"]])

(defmethod ^:private access-request-badge "PENDING" [_]
  [:> Badge {:color "yellow" :size "2"}
   [:> Flex {:gap "1" :align "center"}
    [:> Clock2 {:size 14}]
    "Pending"]])

(defmethod ^:private access-request-badge "REJECTED" [_]
  [:> Badge {:color "red" :size "2"}
   [:> Flex {:gap "1" :align "center"}
    [:> OctagonX {:size 14}]
    "Rejected"]])

(defmethod ^:private access-request-badge :default [_]
  nil)

(defn- workflow-chip
  "Compact, clickable chip linking to the workflow timeline."
  [correlation-id]
  (let [truncated (if (> (count correlation-id) 18)
                    (str (subs correlation-id 0 16) "…")
                    correlation-id)]
    [:> Tooltip {:content (str "View workflow " correlation-id)}
     [:> Button
      {:size "1"
       :variant "soft"
       :on-click (fn [e]
                   (.stopPropagation e)
                   (rf/dispatch [:navigate :workflow-details
                                 {}
                                 :correlation-id
                                 (js/encodeURIComponent correlation-id)]))}
      [:> Workflow {:size 12}]
      truncated]]))

(defn session-item [session]
  (let [user-name (:user_name session)
        review (:review session)
        correlation-id (:correlation_id session)
        has-workflow? (and correlation-id
                           (not (string/blank? correlation-id)))]
    [:> Grid
     {:columns "4"
      :gap "4"
      :align "center"
      :class (str "overflow-hidden border-b cursor-pointer hover:bg-gray-50"
                  " p-regular text-sm")
      :on-click (fn []
                  (rf/dispatch [:modal->open {:id "session-details"
                                              :maxWidth "95vw"
                                              :content [session-details/main session]}]))}

     [:> Flex {:id "user-info"
               :gap "2"
               :align "center"
               :class "min-w-0"}
      [:> Box
       [user-icon/initials-black user-name]]
      [:> Box {:class "truncate text-gray-800 text-xs"}
       user-name]]

     [:> Flex {:id "connection-info"
               :direction "column"
               :gap "1"
               :class "items-end lg:items-start"}
      [:> Box
       [:b (:connection session)]]
      [:> Box {:class "text-xxs text-gray-800"}
       [:> Text (:type session)]]]

     [:> Flex {:id "badge-column"
               :gap "2"
               :align "center"
               :wrap "wrap"}
      (when review
        [access-request-badge (-> session :review :status)])
      (when has-workflow?
        [workflow-chip correlation-id])]

     [:> Flex {:id "status-info"
               :gap "4"
               :align "center"
               :justify "end"
               :class "flex-col-reverse lg:flex-row"}

      [:> Flex {:gap "1"
                :align "center"
                :class "text-xs p-regular rounded-lg bg-gray-100 text-gray-800"}
       [icon/regular {:icon-name "watch-black"
                      :size 4}]
       [:> Box
        (formatters/time-parsed->full-date (:start_date session))]]]]))


