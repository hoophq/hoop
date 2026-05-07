(ns webapp.features.workflows.views.landing
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text TextField]]
   ["lucide-react" :refer [ArrowRight Code2 Globe TerminalSquare Workflow]]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [reagent.core :as r]))

(defn- search-bar []
  (let [value (r/atom "")
        submit (fn []
                 (let [trimmed (string/trim @value)]
                   (when-not (string/blank? trimmed)
                     (rf/dispatch [:navigate :workflow-details
                                   {}
                                   :correlation-id
                                   (js/encodeURIComponent trimmed)]))))]
    (fn []
      (let [disabled? (string/blank? (string/trim @value))]
        [:> Flex {:gap "2" :align "center" :wrap "wrap"
                  :class "w-full"}
         [:> Box {:class "grow min-w-[260px]"}
          [:> TextField.Root
           {:size "3"
            :placeholder "Enter a correlation ID (e.g. order-sync-2026-05-05)"
            :value @value
            :autoFocus true
            :onChange #(reset! value (-> % .-target .-value))
            :onKeyDown (fn [e]
                         (when (= (.-key e) "Enter")
                           (.preventDefault e)
                           (submit)))}
           [:> TextField.Slot
            [:> Workflow {:size 16 :class "text-[--gray-10]"}]]]]
         [:> Button {:size "3"
                     :variant "solid"
                     :color "gray"
                     :highContrast true
                     :disabled disabled?
                     :on-click submit}
          [:> Text {:size "2" :weight "medium"} "Open Workflow"]
          [:> ArrowRight {:size 16}]]]))))

(defn- how-to-card [{:keys [icon title description code]}]
  [:> Box {:class (str "rounded-4 border border-[--gray-a4] bg-white "
                       "p-radix-5 h-full")}
   [:> Flex {:direction "column" :gap "3" :class "h-full"}
    [:> Flex {:align "center" :gap "2"}
     [:> Box {:class (str "flex items-center justify-center w-8 h-8 rounded-3 "
                          "bg-[--gray-3] text-[--gray-12]")}
      [:> icon {:size 16}]]
     [:> Text {:size "2" :weight "bold" :class "text-[--gray-12]"}
      title]]
    [:> Text {:size "2" :class "text-[--gray-11] grow"}
     description]
    [:> Box {:class (str "rounded-2 bg-[--gray-12] px-3 py-2 "
                         "overflow-x-auto")}
     [:> Text {:class "font-mono text-xs text-[--gray-1] whitespace-nowrap"}
      code]]]])

(defn- guidance-section []
  [:> Flex {:direction "column" :gap "6"}
   ;; Centered intro
   [:> Flex {:direction "column" :align "center" :gap "5"
             :class "py-radix-5 text-center"}
    [:> Box {:class "w-56"}
     [:img {:src "/images/illustrations/empty-state.png"
            :alt "Workflows illustration"}]]
    [:> Flex {:direction "column" :gap "2" :class "max-w-xl"}
     [:> Heading {:as "h3" :size "5" :weight "bold" :class "text-[--gray-12]"}
      "See exactly what your automation did"]
     [:> Text {:size "2" :class "text-[--gray-11]"}
      "Workflows correlate every session a script, AI agent, or CI job runs "
      "into a single auditable trail, so you can answer who triggered the run, "
      "what each step touched, and how it moved across your resources."]]]

   ;; Section heading
   [:> Flex {:align "center" :gap "3" :class "mb-radix-1"}
    [:> Text {:size "1" :weight "bold"
              :class "uppercase tracking-wider text-[--gray-11]"}
     "Where to set the correlation ID"]
    [:> Box {:class "h-px grow bg-[--gray-a4]"}]]

   ;; 3 how-to cards
   [:> Box {:class "grid grid-cols-1 md:grid-cols-3 gap-4"}
    [how-to-card
     {:icon TerminalSquare
      :title "CLI"
      :description "Pass the flag to any hoop command that opens or executes a session."
      :code "hoop exec --correlation-id <id> ..."}]
    [how-to-card
     {:icon Globe
      :title "HTTP header"
      :description "Forward the header through the gateway proxy on every request."
      :code "X-Hoop-Correlation-Id: <id>"}]
    [how-to-card
     {:icon Code2
      :title "REST API"
      :description "Set the field on /sessions calls when scripting against the gateway."
      :code "{ \"correlation_id\": \"<id>\" }"}]]])

(defn main []
  [:> Box {:class "min-h-screen bg-gray-1"}
   [:> Box {:class "px-radix-7 pb-radix-7"}
    [:> Box {:class (str "sticky top-0 z-10 bg-[--gray-1] pb-radix-5 "
                         "-mx-radix-7 px-radix-7 pt-radix-7")}
     [:> Heading {:as "h2" :size "8" :class "mb-radix-3"} "Workflows"]
     [:> Text {:size "3" :class "text-[--gray-11]"}
      "Inspect a chain of related sessions grouped by correlation ID."]
     [:> Box {:class "mt-radix-5"}
      [search-bar]]]

    [:> Box {:class "mt-radix-7"}
     [guidance-section]]]])
