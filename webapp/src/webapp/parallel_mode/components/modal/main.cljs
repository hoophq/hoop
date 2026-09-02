(ns webapp.parallel-mode.components.modal.main
  (:require
   ["@radix-ui/themes" :refer [Box Badge Flex Text]]
   ["lucide-react" :refer [FastForward]]
   [re-frame.core :as rf]
   [webapp.components.command-dialog :as command-dialog]
   [webapp.parallel-mode.components.modal.connection-list :as connection-list]
   [webapp.parallel-mode.components.modal.footer :as footer]))

(defn parallel-mode-modal []
  (let [open? (rf/subscribe [:parallel-mode/modal-open?])
        search-term (rf/subscribe [:parallel-mode/search-term])
        selected-count (rf/subscribe [:parallel-mode/selected-count])
        source (rf/subscribe [:parallel-mode/source])
        connections (rf/subscribe [:connections->pagination])]
    (fn []
      ;; Spinner only while there is nothing to show. command-dialog swaps the
      ;; whole list for it, so gating on :loading alone would blank the list on
      ;; every search and every infinite-scroll append.
      (let [loading? (and (:loading @connections)
                          (empty? (:data @connections)))]

        [command-dialog/command-dialog
         {:open? @open?
          :on-open-change (fn [should-open?]
                            (if should-open?
                              (rf/dispatch [:parallel-mode/open-modal @source])
                              (rf/dispatch [:parallel-mode/cancel-selection])))
          :title "Parallel Mode"
          :max-width "max-w-[720px]"
          :height "auto"
          ;; Taller than the other pickers: this one lists the selection and the
          ;; results at the same time. 10vh top + 78vh box leaves 12vh below.
          :offset-top "pt-[10vh]"
          :class-name "h-[78vh] min-h-[480px] max-h-[820px]"
          ;; The gateway does the matching. cmdk's own filter is a fuzzy
          ;; subsequence scorer and only sees the page already fetched. EVL-243.
          :should-filter? false
          :loading? loading?

          :search-config {:show-search-icon true
                          :show-input true
                          :placeholder "Search resource roles..."
                          :value @search-term
                          :on-value-change #(rf/dispatch [:parallel-mode/set-search-term %])
                          :on-key-down (fn [e]
                                         (when (= (.-key e) "Escape")
                                           (.preventDefault e)
                                           (rf/dispatch [:parallel-mode/cancel-selection])))}

          :breadcrumb-component (fn []
                                  [:> Flex
                                   {:align "center"
                                    :gap "2"
                                    :class "bg-success-3 px-2 py-1 rounded-full"
                                    :role "status"
                                    :aria-label (str @selected-count " resource role" 
                                                    (when (not= 1 @selected-count) "s") 
                                                    " selected for parallel execution")}
                                   [:> FastForward {:size 16 :class "text-success-11" :aria-hidden "true"}]
                                   [:> Text {:size "2" :weight "medium" :class "text-success-11"}
                                    "Parallel Mode"]
                                   [:> Badge {:variant "solid"
                                              :color "green"
                                              :radius "full"
                                              :size "1"
                                              :aria-hidden "true"}
                                    @selected-count]])

          :content
          [:<>
           ;; Live region for selection changes
           [:div {:class "sr-only"
                  :role "status"
                  :aria-live "polite"
                  :aria-atomic "true"}
            (str @selected-count " resource role" 
                 (when (not= 1 @selected-count) "s") 
                 " selected")]
           
           ;; Scrollable list. The empty state lives in connection-list, which is
           ;; the only place that knows whether the Selected group is holding
           ;; rows that the search does not match. cmdk's CommandEmpty counts
           ;; every mounted item, so it would stay hidden in that case.
           [:> Box {:class "flex-1 overflow-y-auto"}
            [connection-list/main]]

           [footer/main]]}]))))
