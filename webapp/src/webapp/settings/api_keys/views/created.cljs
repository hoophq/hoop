(ns webapp.settings.api-keys.views.created
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading Text]]
   ["lucide-react" :refer [CheckCircle Copy Info]]
   [re-frame.core :as rf]
   [reagent.core :as r]))

(defn main []
  (let [created (rf/subscribe [:api-keys/created])
        copied? (r/atom false)]
    (fn []
      (let [raw-key (get-in @created [:data :key])]
        [:> Box {:class "bg-gray-1 min-h-screen flex items-center justify-center p-7"}
         [:> Box {:class "max-w-lg w-full space-y-radix-8"}
          [:> Flex {:direction "column" :align "center" :gap "3"}
           [:> Box {:class "p-4 bg-[--accent-3] rounded-full"}
            [:> CheckCircle {:size 40 :class "text-[--accent-9]"}]]
           [:> Heading {:as "h2" :size "7" :align "center"} "Your API key is ready!"]]

          [:> Callout.Root {:color "amber" :variant "soft"}
           [:> Callout.Icon
            [:> Info {:size 16}]]
           [:> Callout.Text
            "This is the only time you'll see this key. Copy and store it securely."]]

          [:> Box {:class "space-y-radix-2"}
           [:> Text {:size "2" :weight "bold" :class "text-[--gray-11]"} "Copy your API Key"]
           [:> Flex {:gap "2" :align "center"}
            [:> Box {:class "flex-1 font-mono text-sm bg-[--gray-2] border border-[--gray-6] rounded-lg p-3 overflow-x-auto whitespace-nowrap"}
             (or raw-key "")]
            [:> Button {:variant "soft"
                        :color (if @copied? "green" "gray")
                        :size "2"
                        :on-click (fn []
                                    (when raw-key
                                      (.then (js/navigator.clipboard.writeText raw-key)
                                             (fn []
                                               (reset! copied? true)
                                               (js/setTimeout #(reset! copied? false) 2000)))))}
             [:> Copy {:size 14}]
             (if @copied? "Copied!" "Copy")]]]

          [:> Flex {:justify "center"}
           [:> Button {:variant "ghost"
                       :size "2"
                       :on-click (fn []
                                   (rf/dispatch [:api-keys/clear-created])
                                   (rf/dispatch [:navigate :settings-api-keys]))}
            "← Back to API keys"]]]]))))
