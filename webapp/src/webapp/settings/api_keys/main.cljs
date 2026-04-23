(ns webapp.settings.api-keys.main
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   [re-frame.core :as rf]
   [webapp.components.loaders :as loaders]
   [webapp.settings.api-keys.views.empty-state :as empty-state]
   [webapp.settings.api-keys.views.list :as api-keys-list]))

(defn main []
  (let [loading   (rf/subscribe [:api-keys/loading?])
        api-keys  (rf/subscribe [:api-keys/list-data])]
    (rf/dispatch [:api-keys/list])

    (fn []
      (let [loading?  @loading
            list-data @api-keys]
        [:> Box {:class "bg-gray-1 p-radix-7 min-h-full h-max"}
         [:> Flex {:direction "column" :gap "6" :class "h-full"}
          [:> Flex {:justify "between" :align "center" :class "mb-6"}
           [:> Flex {:class "flex-col gap-2"}
            [:> Heading {:size "8" :weight "bold" :as "h1"} "API Keys"]
            [:> Text {:size "5" :class "text-[--gray-11]"}
             "Create and manage API Keys"]]
           (when (seq list-data)
             [:> Button {:size "3"
                         :on-click #(rf/dispatch [:navigate :settings-api-keys-new])}
              "Create new API key"])]

          (cond
            loading?
            [:> Box {:class "bg-gray-1 h-full"}
             [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
              [loaders/simple-loader]]]

            (empty? list-data)
            [empty-state/main]

            :else
            [api-keys-list/main])]]))))
