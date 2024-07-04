(ns webapp.plugins.views.plugin-configurations.ask-ai
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.headings :as h]
            [webapp.components.toggle :as toggle]))

(defn main []
  (let [user (rf/subscribe [:users->current-user])
        enabled? (r/atom (if (= (get-in @user [:data :feature_ask_ai] "disabled") "enabled")
                           true
                           false))]
    (fn []
      (let [feature-ai-ask (get-in @user [:data :feature_ask_ai] "disabled")]
        [:<>
         [:section {:class (when (= feature-ai-ask "unavailable")
                             "opacity-50 pointer-events-none")}
          [:div {:class "grid grid-cols-2 items-center gap-large mt-large"}
           [:div
            [h/h3 "Enable AI Query Builder" {:class "text-gray-800"}]
            [:span {:class "block text-sm mb-regular text-gray-600"}
             "By activating this feature you allow us to send non-identifiable raw database schemas to GPT-4."]]
           [toggle/main {:enabled? @enabled?
                         :disabled? (= feature-ai-ask "unavailable")
                         :on-click (fn []
                                     (rf/dispatch [:ask-ai->set-config
                                                   (if (not @enabled?) "enabled" "disabled")])
                                     (reset! enabled? (not @enabled?)))}]]]
         (when (= feature-ai-ask "unavailable")
           [:span {:class "block text-sm mt-regular text-gray-600"}
            "The GPT key has not been configured. To release the feature, please configure the GPT key in your gateway."])]))))
