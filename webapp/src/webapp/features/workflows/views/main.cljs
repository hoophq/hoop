(ns webapp.features.workflows.views.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Text]]
   ["lucide-react" :refer [Workflow]]
   [re-frame.core :as rf]
   [webapp.features.workflows.events]
   [webapp.features.workflows.subs]
   [webapp.features.workflows.views.header :as header]
   [webapp.features.workflows.views.timeline :as timeline]))

(defn- page-shell
  "Atmospheric page background. The page is clean and editorial — a faint
   dotted veil at the very top of the viewport gives the dark hero card
   something to breathe against."
  [& children]
  [:> Box {:class "relative h-full overflow-y-auto bg-[--gray-1]"}
   ;; Decorative dot grid that fades out below the hero
   [:> Box {:aria-hidden "true"
            :class "pointer-events-none absolute inset-x-0 top-0 h-[420px]"
            :style {:backgroundImage
                    "radial-gradient(circle at 1px 1px, var(--gray-a4) 1px, transparent 0)"
                    :backgroundSize "20px 20px"
                    :maskImage "linear-gradient(to bottom, black, transparent)"
                    :WebkitMaskImage "linear-gradient(to bottom, black, transparent)"}}]
   (into [:> Box {:class "relative flex flex-col px-4 py-10 sm:px-6 lg:p-10"}]
         children)])

(defn- loading-skeleton []
  [:> Flex {:direction "column" :gap "5"}
   [:> Box {:class "h-44 rounded-6 bg-[--gray-3] animate-pulse"}]
   [:> Flex {:direction "column" :gap "3"}
    (for [i (range 4)]
      ^{:key i}
      [:> Flex {:gap "4" :align "center"}
       [:> Box {:class "w-8 h-8 rounded-full bg-[--gray-3] animate-pulse shrink-0"}]
       [:> Box {:class "grow h-16 rounded-4 bg-[--gray-3] animate-pulse"}]])]])

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
     "Try again, or head back to the sessions list."]]
   [:> Flex {:gap "3"}
    [:> Button {:size "2" :variant "soft" :color "gray"
                :on-click #(rf/dispatch [:workflows/get correlation-id])}
     "Retry"]
    [:> Button {:size "2" :variant "outline" :color "gray"
                :on-click #(rf/dispatch [:navigate :sessions])}
     "Back to sessions"]]])

(defn- empty-state [correlation-id]
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
     "Make sure your agent passes the same correlation ID on every step — either "
     "with the "
     [:code {:class "font-mono text-[--gray-12]"} "--correlation-id"]
     " CLI flag, the "
     [:code {:class "font-mono text-[--gray-12]"} "X-Hoop-Correlation-Id"]
     " HTTP header, or the "
     [:code {:class "font-mono text-[--gray-12]"} "correlation_id"]
     " field on the REST API."]]
   [:> Button {:size "2" :variant "soft" :color "gray"
               :on-click #(rf/dispatch [:navigate :sessions])}
    "Back to sessions"]])

(defn page
  "Top-level page component for `/sessions/workflows/:correlation-id`.
   Data fetching is triggered from the route panel multimethod, so this
   component just renders the current `:workflows` state."
  [correlation-id]
  (let [status @(rf/subscribe [:workflows/status])
        summary @(rf/subscribe [:workflows/summary])
        sessions @(rf/subscribe [:workflows/sessions])]
    [page-shell
     [:> Box {:class "max-w-5xl w-full mx-auto space-y-radix-7"}
      (case status
        :loading [loading-skeleton]
        :error   [error-state correlation-id]
        (:idle :ready)
        [:<>
         [header/header correlation-id summary]
         (if (zero? (count sessions))
           [empty-state correlation-id]
           [timeline/timeline])]
        [loading-skeleton])]]))
