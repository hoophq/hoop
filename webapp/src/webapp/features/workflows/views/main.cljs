(ns webapp.features.workflows.views.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading IconButton Text Tooltip]]
   ["lucide-react" :refer [ArrowLeft Workflow]]
   [re-frame.core :as rf]
   [webapp.features.workflows.events]
   [webapp.features.workflows.subs]
   [webapp.features.workflows.views.header :as header]
   [webapp.features.workflows.views.timeline :as timeline]))

(defn- breadcrumb []
  [:> Flex {:align "center" :gap "2"}
   [:> Tooltip {:content "Back to Sessions"}
    [:> IconButton {:size "2" :variant "ghost" :color "gray"
                    :on-click #(rf/dispatch [:navigate :sessions])}
     [:> ArrowLeft {:size 16}]]]
   [:> Flex {:align "center" :gap "1"}
    [:> Text {:size "2" :weight "medium" :class "text-[--gray-11]"
              :as "button"
              :role "link"
              :style {:cursor "pointer"}
              :on-click #(rf/dispatch [:navigate :sessions])}
     "Sessions"]
    [:> Text {:size "2" :class "text-[--gray-9]"} "/"]
    [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"}
     "Workflow Timeline"]]])

(defn- loading-skeleton []
  [:> Flex {:direction "column" :gap "5"}
   [:> Box {:class "h-32 rounded-4 bg-[--gray-3] animate-pulse"}]
   [:> Flex {:direction "column" :gap "2"}
    (for [i (range 4)]
      ^{:key i}
      [:> Box {:class "h-14 rounded-3 bg-[--gray-3] animate-pulse"}])]])

(defn- error-state [correlation-id]
  [:> Flex {:direction "column" :align "center" :justify "center"
            :gap "5"
            :class "py-16 text-center"}
   [:> Box {:class "w-72"}
    [:img {:src "/images/illustrations/empty-state.png"
           :alt "Workflow load error"}]]
   [:> Flex {:direction "column" :align "center" :gap "2"}
    [:> Text {:size "4" :weight "bold" :class "text-[--gray-12]"}
     "We couldn't load this workflow"]
    [:> Text {:size "2" :class "text-[--gray-11]"}
     "Try again, or head back to Sessions."]]
   [:> Flex {:gap "3"}
    [:> Button {:size "2" :variant "soft" :color "gray"
                :on-click #(rf/dispatch [:workflows/get correlation-id])}
     "Retry"]
    [:> Button {:size "2" :variant "outline" :color "gray"
                :on-click #(rf/dispatch [:navigate :sessions])}
     "Back to Sessions"]]])

(defn- empty-state [_]
  [:> Flex {:direction "column" :align "center" :justify "center"
            :gap "5"
            :class "py-16 text-center"}
   [:> Box {:class (str "flex items-center justify-center w-16 h-16 "
                        "rounded-full bg-[--accent-3] text-[--accent-11]")}
    [:> Workflow {:size 28}]]
   [:> Flex {:direction "column" :align "center" :gap "2" :class "max-w-md"}
    [:> Text {:size "4" :weight "bold" :class "text-[--gray-12]"}
     "No sessions found for this workflow"]
    [:> Text {:size "2" :class "text-[--gray-11]"}
     "Make sure your AI agent or Automation passes the same correlation ID on every step — either "
     "with the "
     [:code {:class "text-[--gray-12]"} "--correlation-id"]
     " CLI flag, the "
     [:code {:class "text-[--gray-12]"} "X-Hoop-Correlation-Id"]
     " HTTP header, or the "
     [:code {:class "text-[--gray-12]"} "correlation_id"]
     " field on the REST API."]]
   [:> Button {:size "2" :variant "soft" :color "gray"
               :on-click #(rf/dispatch [:navigate :sessions])}
    "Back to Sessions"]])

(defn page
  "Top-level page component for `/workflows/:correlation-id`."
  [correlation-id]
  (let [status @(rf/subscribe [:workflows/status])
        summary @(rf/subscribe [:workflows/summary])
        sessions @(rf/subscribe [:workflows/sessions])]
    [:> Box {:class "min-h-screen bg-gray-1"}
     [:> Box {:class "px-radix-7 pb-radix-7"}
      [:> Box {:class (str "sticky top-0 z-10 bg-[--gray-1] pb-radix-5 "
                           "-mx-radix-7 px-radix-7 pt-radix-7")}
       [:> Box {:class "mb-radix-3"}
        [breadcrumb]]
       [:> Heading {:as "h2" :size "8" :class "mb-radix-3"} "Workflow Timeline"]
       [:> Text {:size "3" :class "text-[--gray-11]"}
        "Inspect each session that ran with this correlation ID."]]

      [:> Box {:class "mt-radix-5 space-y-radix-6"}
       (case status
         :loading [loading-skeleton]
         :error   [error-state correlation-id]
         (:idle :ready)
         [:<>
          [header/header correlation-id summary]
          (if (zero? (count sessions))
            [empty-state correlation-id]
            [timeline/timeline])]
         [loading-skeleton])]]]))
