(ns webapp.webclient.log-area.logs
  (:require ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["@radix-ui/themes" :refer [Box Spinner Flex Text DropdownMenu]]
            ["lucide-react" :refer [SquareArrowOutUpRight EllipsisVertical Copy]]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [webapp.audit.views.session-details :as session-details]
            [webapp.formatters :as formatters]))

(defn action-buttons-container [session-id logs-content]
  [:div {:class "sticky top-1 right-0 h-0 w-full z-30"
         :style {:pointer-events "none"}}
   [:div {:class "absolute top-0 -right-4"
          :style {:pointer-events "auto"}}
    [:> DropdownMenu.Root
     [:> DropdownMenu.Trigger {:class (str "cursor-pointer p-1.5 rounded-full "
                                           "bg-gray-3 hover:bg-gray-5 shadow-sm "
                                           "opacity-100 transition border border-gray-5")}
      [:> Box
       [:> EllipsisVertical {:size 18 :class "text-gray-12"}]]]
     [:> DropdownMenu.Content
      [:> DropdownMenu.Item {:on-select #(rf/dispatch [:open-modal
                                                       [session-details/main {:id session-id :verb "exec"}]
                                                       :large
                                                       (fn []
                                                         (rf/dispatch [:audit->clear-session])
                                                         (rf/dispatch [:close-modal]))])}
       [:> Flex {:align "center" :gap "2"}
        [:> SquareArrowOutUpRight {:size 16}]
        [:> Text {:size "2"} "View session details"]]]
      [:> DropdownMenu.Item {:on-select #(js/navigator.clipboard.writeText logs-content)}
       [:> Flex {:align "center" :gap "2"}
        [:> Copy {:size 16}]
        [:> Text {:size "2"} "Copy logs content"]]]]]]])

(defn- logs-area-list
  [status {:keys [logs logs-status execution-time has-review? session-id]}]
  (case status
    :success (if has-review?
               [:div {:class "group relative py-regular pl-regular pr-large whitespace-pre-wrap"
                      :on-click (fn []
                                  (rf/dispatch [:open-modal
                                                [session-details/main {:id session-id :verb "exec"}]
                                                :large
                                                (fn []
                                                  (rf/dispatch [:audit->clear-session])
                                                  (rf/dispatch [:close-modal]))]))}
                [action-buttons-container session-id "This task needs to be reviewed. Please click here to see the details."]
                [:div {:class "text-sm mb-1"}
                 "This task needs to be reviewed. Please click here to see the details."]
                [:div {:class "text-gray-11 text-sm"}
                 (str (formatters/current-time) " [cost " (formatters/time-elapsed execution-time) "]")]]

               [:div {:class " group relative py-regular pl-regular pr-large whitespace-pre-wrap"}
                [action-buttons-container session-id logs]
                [:div {:class "text-sm mb-1"}
                 logs]
                [:div {:class (str (if (= logs-status "success")
                                     "text-gray-11 text-sm"
                                     "text-gray-11 text-sm"))}
                 (str (formatters/current-time) " [cost " (formatters/time-elapsed execution-time) "]")]])
    :loading [:div {:class "flex gap-regular py-regular pl-regular pr-large"}
              [:> Spinner {:loading true}]
              [:span "loading"]]
    :failure [:div {:class " group relative py-regular pl-regular pr-large whitespace-pre-wrap"}
              [action-buttons-container session-id "There was an error to get the logs for this task"]
              [:div {:class "text-sm mb-1"}
               "There was an error to get the logs for this task"]
              [:div {:class "text-gray-11 text-sm"}
               (str (formatters/current-time) " [cost " (formatters/time-elapsed execution-time) "]")]]
    [:div {:class "flex gap-regular py-regular pl-regular pr-large"}
     [:span  "No logs to show"]]))

(defn main
  "config is a map with the following fields:
  :status -> possible values are :success :loading :failure. Anything different will be default to an generic error message
  :id -> id to differentiate more than one log on the same page.
  :logs -> the actual string with the logs"
  [type config]
  [:section
   {:class (str "relative bg-gray-2 font-mono h-full"
                " whitespace-pre text-gray-11 text-sm overflow-y-auto"
                " h-full")
    :style {:overflow-anchor "none"}}
   (case type
     :logs
     [logs-area-list (:status config)
      {:logs (:response config)
       :logs-status (:response-status config)
       :script (:script config)
       :execution-time (:execution-time config)
       :has-review? (:has-review config)
       :session-id (:response-id config)}])])
