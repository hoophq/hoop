(ns webapp.dashboard.main
  (:require ["@radix-ui/themes" :refer [Box Flex Grid Heading Section Strong
                                        Text]]
            [re-frame.core :as rf]
            [webapp.dashboard.coming-soon :as coming-soon]
            [webapp.dashboard.connection-chart :as connection-chart]
            [webapp.dashboard.redacted-data-chart :as redacted-data-chart]
            [webapp.dashboard.review-chart :as review-chart]))

(defn main []
  (let [reports->today-redacted-data (rf/subscribe [:reports->today-redacted-data])
        reports->today-review-data (rf/subscribe [:reports->today-review-data])
        reports->today-session-data (rf/subscribe [:reports->today-session-data])
        redacted-data (rf/subscribe [:reports->redacted-data-by-date])
        reviews (rf/subscribe [:reports->review-data-by-date])
        connections (rf/subscribe [:connections])]
    (rf/dispatch [:connections->get-connections])
    (rf/dispatch [:reports->get-today-redacted-data])
    (rf/dispatch [:reports->get-today-review-data])
    (rf/dispatch [:reports->get-today-session-data])
    (rf/dispatch [:reports->get-redacted-data-by-date 7])
    (rf/dispatch [:reports->get-review-data-by-date 7])
    (fn []
      [:<>
       [:> Section {:size "1"}
        [:> Box {:p "5" :class "bg-white rounded-t-md border border-gray-100"}
         [:> Heading {:as "h2" :size "3"}
          "Today's overview"]]
        [:> Flex {:gap "1" :p "5" :justify "between" :class "bg-white rounded-b-md border border-gray-100"}
         [:> Box {:minWidth "300px"}
          [:> Heading {:as "h3" :size "2"}
           "Sessions"]
          [:> Text {:as "label" :color "gray" :weight "light" :size "1"}
           "reviewed and safely executed"]
          [:> Text {:as "p" :size "8"}
           [:> Strong
            (-> @reports->today-session-data
                :data
                :total)]]]
         [:> Box {:minWidth "300px"}
          [:> Heading {:as "h3" :size "2"}
           "Reviews"]
          [:> Text {:as "label" :color "gray" :weight "light" :size "1"}
           "sent via safe channels"]
          [:> Text {:as "p" :size "8"}
           [:> Strong
            (count (:data @reports->today-review-data))]]]
         [:> Box {:minWidth "300px"}
          [:> Heading {:as "h3" :size "2"}
           "Redacted Data"]
          [:> Text {:as "label" :color "gray" :weight "light" :size "1"}
           "protected with AI Data Masking"]
          [:> Text {:as "p" :size "8"}
           [:> Strong
            (-> @reports->today-redacted-data :data :total_redact_count)]]]]]

       [:> Grid {:gap "5" :columns "2"}
        [:> Section {:size "1" :p "5" :class "bg-white rounded-md"}
         [:> Flex {:direction "column" :height "100%"}
          [:> Box
           [:> Heading {:as "h3" :size "2"}
            "Sessions"]]

          [coming-soon/main]]]

        [review-chart/main reviews]]

       [redacted-data-chart/main redacted-data]

       [:> Grid {:gap "5" :columns "2"}
        [:> Section {:size "1" :p "5" :class "bg-white rounded-md"}
         [:> Flex {:direction "column" :height "100%"}
          [:> Box
           [:> Heading {:as "h3" :size "2"}
            "Runbooks"]]

          [coming-soon/main]]]

        [connection-chart/main connections]]])))
