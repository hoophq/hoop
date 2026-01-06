(ns webapp.parallel-mode.components.modal.main
  (:require
   ["cmdk" :refer [CommandEmpty]]
   ["@radix-ui/themes" :refer [Box Badge Flex Text]]
   ["lucide-react" :refer [FastForward]]
   [re-frame.core :as rf]
   [webapp.components.command-dialog :as command-dialog]
   [webapp.parallel-mode.components.modal.connection-list :as connection-list]
   [webapp.parallel-mode.components.modal.footer :as footer]))

(defn parallel-mode-modal []
  (let [open? @(rf/subscribe [:parallel-mode/modal-open?])
        search-term @(rf/subscribe [:parallel-mode/search-term])
        selected-count @(rf/subscribe [:parallel-mode/selected-count])
        connections @(rf/subscribe [:connections->pagination])
        loading? (= :loading (:loading connections))]

    [command-dialog/command-dialog
     {:open? open?
      :on-open-change (fn [should-open?]
                        (if should-open?
                          (rf/dispatch [:parallel-mode/open-modal])
                          (rf/dispatch [:parallel-mode/close-modal])))
      :title "Parallel Mode"
      :max-width "max-w-[640px]"
      :height "auto"
      :class-name "h-[480px]"
      :should-filter? true
      :loading? loading?

      :search-config {:show-search-icon true
                      :show-input true
                      :placeholder "Search resource roles..."
                      :value search-term
                      :on-value-change #(rf/dispatch [:parallel-mode/set-search-term %])
                      :on-key-down (fn [e]
                                     (when (= (.-key e) "Escape")
                                       (.preventDefault e)
                                       (rf/dispatch [:parallel-mode/close-modal])))}

      :breadcrumb-component (fn []
                              [:> Flex
                               {:align "center"
                                :gap "2"
                                :class "bg-success-3 px-2 py-1 rounded-full"}
                               [:> FastForward {:size 16 :class "text-success-11"}]
                               [:> Text {:size "2" :weight "medium" :class "text-success-11"}
                                "Parallel Mode"]
                               [:> Badge {:variant "solid"
                                          :color "green"
                                          :radius "full"
                                          :size "1"}
                                selected-count]])

      :content
      [:<>
       ;; Scrollable list
       [:> Box {:class "flex-1 overflow-y-auto"}
        [connection-list/main]

        [:> CommandEmpty
         [:> Flex {:direction "column" :align "center" :gap "2" :class "py-8"}
          [:> Text {:size "2" :color "gray"} "No resource roles found"]]]]

       [footer/main]]}]))
