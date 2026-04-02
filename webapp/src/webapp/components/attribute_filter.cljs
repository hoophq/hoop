(ns webapp.components.attribute-filter
  (:require
  ["@radix-ui/themes" :refer [Popover Button TextField Text Flex Box]]
  ["lucide-react" :refer [Check ListVideo Search X]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]))

(defn main
  "Reusable list filter for selecting one attribute.

   Props:
   - :selected (string)
   - :on-select (fn [attribute-name])
   - :on-clear (fn [])
   - :label (string, optional)
   - :placeholder (string, optional)"
  [_]
  (let [open? (r/atom false)
        search-term (r/atom "")
        attributes (rf/subscribe [:attributes/list-data])]
    (rf/dispatch [:attributes/list])

    (fn [{:keys [selected on-select on-clear label placeholder]
          :or {label "Attributes"
               placeholder "Search attributes"}}]
      (let [all-attributes (or @attributes [])
            selected-name (cond
                            (string? selected) selected
                            (and (sequential? selected) (string? (first selected))) (first selected)
                            :else nil)
            has-selected? (and (string? selected-name) (not (cs/blank? selected-name)))
            query (cs/lower-case (cs/trim @search-term))
            filtered-attributes (if (cs/blank? query)
                                  all-attributes
                                  (filterv (fn [attr]
                                             (cs/includes? (cs/lower-case (or (:name attr) "")) query))
                                           all-attributes))
            close! #(reset! open? false)]
        [:> Popover.Root {:open @open?
                          :on-open-change #(reset! open? %)}
         [:> Popover.Trigger {:asChild true}
          [:> Button {:size "2"
                      :variant (if has-selected? "soft" "surface")
                      :color "gray"
                      :class "gap-2"}
           [:> ListVideo {:size 14}]
           [:> Text {:size "2" :weight "medium"}
            (if has-selected? selected-name label)]
           (when has-selected?
             [:<>
              [:> Flex {:class "items-center justify-center rounded-full h-4 w-4 bg-[--accent-9]"}
               [:span {:class "text-white text-xs font-bold"} "1"]]
              [:> X {:size 14
                     :on-click (fn [e]
                                 (.stopPropagation e)
                                 (on-clear)
                                 (close!))}]])]]
         [:> Popover.Content {:size "2" :style {:width "360px" :max-height "400px"}}
          [:> Box
           (when has-selected?
             [:> Box {:mb "2" :pb "2" :class "border-b border-[--gray-a6]"}
              [:> Flex {:align "center"
                        :gap "2"
                        :class "cursor-pointer text-[--gray-11] hover:bg-[--gray-a3] rounded px-3 py-2"
                        :on-click (fn []
                                    (on-clear)
                                    (close!))}
               [:> Text {:size "2"} "Clear filter"]]])

           [:> Box {:mb "2"}
            [:> TextField.Root {:placeholder placeholder
                                :value @search-term
                                :onChange #(reset! search-term (-> % .-target .-value))}
             [:> TextField.Slot
              [:> Search {:size 14}]]]]

           (if (seq filtered-attributes)
             [:> Box {:class "max-h-72 overflow-y-auto"}
              (doall
               (for [attr filtered-attributes]
                 (let [attr-name (:name attr)]
                   ^{:key attr-name}
                   [:> Flex {:align "center"
                             :justify "between"
                             :gap "2"
                             :class "cursor-pointer hover:bg-[--gray-a3] rounded px-3 py-2"
                             :on-click (fn []
                                         (on-select attr-name)
                                         (close!))}
                    [:> Text {:size "2" :class "truncate"}
                     attr-name]
                    (when (= attr-name selected-name)
                      [:> Check {:size 14}])])))]
             [:> Box {:px "3" :py "4"}
              [:> Text {:size "1" :class "text-[--gray-11] italic"}
               (if (seq @search-term)
                 "No attributes found"
                 "No attributes available")]])]]]))))
