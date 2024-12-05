(ns webapp.connections.views.create-update-connection.connection-details-form
  (:require
   ["@radix-ui/themes" :refer [Box Callout Flex Grid Link Switch Text]]
   ["lucide-react" :refer [ArrowUpRight Star]]
   [re-frame.core :as rf]
   [webapp.components.forms :as forms]
   [webapp.components.multiselect :as multi-select]
   [webapp.connections.dlp-info-types :as dlp-info-types]
   [webapp.connections.helpers :as helpers]))

(defn main
  [{:keys [user-groups
           free-license?
           connection-name
           connection-subtype
           form-type
           reviews
           review-groups
           ai-data-masking
           ai-data-masking-info-types]}]
  [:> Flex {:direction "column" :gap "9" :class "px-20"}
   [:> Grid {:columns "5" :gap "7"}
    [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
     [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Connection information"]
     [:> Text {:size "3" :class "text-gray-11"} "Names are used to identify your connection and can't be changed."]]
    [:> Box {:class "space-y-radix-5" :grid-column "span 3 / span 3"}
     [forms/input {:placeholder (str (when @connection-subtype
                                       (str @connection-subtype "-"))
                                     (helpers/random-connection-name))
                   :label "Name"
                   :required true
                   :disabled (= form-type :update)
                   :value @connection-name
                   :on-change #(reset! connection-name (-> % .-target .-value))}]]]
   [:> Grid {:columns "5" :gap "7"}
    [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
     [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Configuration parameters"]
     [:> Text {:size "3" :class "text-gray-11"} "Setup how users interact with this connection."]
     [:> Link {:href "https://hoop.dev/docs/learn/jit-reviews"
               :target "_blank"}
      [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
       [:> Callout.Icon
        [:> ArrowUpRight {:size 16}]]
       [:> Callout.Text
        "Learn more about Reviews"]]]

     [:> Link {:href "https://hoop.dev/docs/learn/ai-data-masking"
               :target "_blank"}
      [:> Callout.Root {:size "1" :mt "4" :variant "outline" :color "gray" :class "w-fit"}
       [:> Callout.Icon
        [:> ArrowUpRight {:size 16}]]
       [:> Callout.Text
        "Learn more about AI Data Masking"]]]]

    [:> Flex {:direction "column" :gap "7" :grid-column "span 3 / span 3"}
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:checked @reviews
                   :size "3"
                   :onCheckedChange #(reset! reviews %)}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "Reviews"]
        [:> Text {:as "p" :size "2"} (str "Require approval prior to connection execution. "
                                          "Enable Just-in-Time access for 30-minute sessions or Command reviews "
                                          "for individual query approvals.")]
        (when @reviews
          [:> Box {:mt "4"}
           [multi-select/main {:options (helpers/array->select-options @user-groups)
                               :id "approval-groups-input"
                               :name "approval-groups-input"
                               :required? @reviews
                               :default-value (if @reviews
                                                @review-groups
                                                nil)
                               :on-change #(reset! review-groups (js->clj %))}]])]]]
     [:> Box {:class "space-y-radix-5"}
      [:> Flex {:align "center" :gap "5"}
       [:> Switch {:checked @ai-data-masking
                   :size "3"
                   :onCheckedChange #(reset! ai-data-masking %)
                   :disabled free-license?}]
       [:> Box
        [:> Text {:as "h4" :size "3" :weight "medium"} "AI Data Masking"]
        [:> Text {:as "p" :size "2"} (str "Provide an additional layer of security by ensuring "
                                          "sensitive data is masked in query results with AI-powered data masking.")]
        (when free-license?
          [:> Callout.Root {:size "2" :mt "4" :mb "4"}
           [:> Callout.Icon
            [:> Star {:size 16}]]
           [:> Callout.Text {:class "text-gray-12"}
            "Enable AI Data Masking by "
            [:> Link {:href "#"
                      :class "text-primary-10"
                      :on-click #(rf/dispatch [:navigate :upgrade-plan])}
             "upgrading your plan."]]])
        (when @ai-data-masking
          [:> Box {:mt "4"}
           [multi-select/main {:options (helpers/array->select-options dlp-info-types/options)
                               :id "data-masking-groups-input"
                               :name "data-masking-groups-input"
                               :disabled? (or (not @ai-data-masking) free-license?)
                               :required? @ai-data-masking
                               :default-value (if @ai-data-masking
                                                @ai-data-masking-info-types
                                                nil)
                               :on-change #(reset! ai-data-masking-info-types (js->clj %))}]])]]]]]])
