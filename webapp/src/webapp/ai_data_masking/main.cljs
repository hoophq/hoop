(ns webapp.ai-data-masking.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading Link Text]]
   ["lucide-react" :refer [AlertCircle]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.features.promotion :as promotion]
   [webapp.ai-data-masking.rule-list :as rule-list]))

(defn main []
  (let [ai-data-masking-list (rf/subscribe [:ai-data-masking->list])
        min-loading-done (r/atom false)
        gateway-info (rf/subscribe [:gateway->info])
        user (rf/subscribe [:users->current-user])]
    (rf/dispatch [:ai-data-masking->get-all])
    (rf/dispatch [:connections->get-connections])

    ;; Set timer for minimum loading time
    (js/setTimeout #(reset! min-loading-done true) 1500)

    (fn []
      (let [loading? (or (= :loading (:status @ai-data-masking-list))
                         (not @min-loading-done))
            redact-provider (-> @gateway-info :data :redact_provider)
            free-license? (-> @user :data :free-license?)
            rules (:data @ai-data-masking-list)
            limit-reached? (and free-license? (>= (count rules) 1))]
        (cond
          loading?
          [:> Flex {:height "100%" :direction "column" :gap "5"
                    :class "bg-gray-1" :align "center" :justify "center"}
           [loaders/simple-loader]]

          (empty? rules)
          [:> Box {:class "bg-gray-1 h-full"}
           [promotion/ai-data-masking-promotion {:mode :empty-state
                                                 :redact-provider redact-provider}]]

          :else
          [:> Box {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
           [:header {:class "mb-7"}
            [:> Flex {:justify "between" :align "center"}
             [:> Box
              [:> Heading {:size "8" :weight "bold" :as "h1"}
               "AI Data Masking"]
              [:> Text {:size "5" :class "text-[--gray-11]"}
               "Automatically mask sensitive data in real-time at the protocol layer"]]

             [:> Button {:size "3"
                         :variant "solid"
                         :disabled limit-reached?
                         :on-click #(rf/dispatch [:navigate :create-ai-data-masking])}
              "Create new"]]]

           (when limit-reached?
             [:> Callout.Root {:color "red" :class "mb-5"}
              [:> Callout.Icon
               [:> AlertCircle {:size 16}]]
              [:> Callout.Text
               "Your organization has reached AI Data Masking free usage limits. Upgrade to Enterprise to keep your sensitive data protected. "
               [:> Link {:href "#"
                         :class "font-medium"
                         :color "red"
                         :on-click (fn [e]
                                     (.preventDefault e)
                                     (promotion/request-demo))}
                "Contact our Sales team \u2197"]]])

           [rule-list/main
            {:rules rules
             :on-configure #(rf/dispatch [:navigate :edit-ai-data-masking {} :ai-data-masking-id %])}]])))))
