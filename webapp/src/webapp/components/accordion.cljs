(ns webapp.components.accordion
  (:require
   ["@radix-ui/react-accordion" :as Accordion]
   ["@radix-ui/themes" :refer [Box Flex Text Avatar Checkbox Button Badge]]
   ["lucide-react" :refer [Check ChevronRight]]
   [reagent.core :as r]))

(defn badge [text]
  [:> Badge {:color "red" :variant "soft" :size "2"} text])

(defn configure-button []
  [:> Button {:variant "soft" :size "3"} "Configure"])

(defn checkbox []
  [:> Checkbox])

(defn status-icon []
  [:> Avatar {:size "1"
              :variant "soft"
              :color "green"
              :radius "full"
              :fallback (r/as-element
                         [:> Check {:size 16
                                    :color "green"}])}])

(defn accordion-item [{:keys [title subtitle content value status show-checkbox? show-badge? show-configure? show-icon? total-items]}]
  [:> (.-Item Accordion)
   {:value value
    :className (str "first:rounded-t-lg last:rounded-b-lg bg-[--accent-2] border-[--gray-a6] "
                    (when (> total-items 1) "border first:border-b-0 last:border-t-0")
                    (when (= total-items 1) "border"))}
   [:> (.-Header Accordion) {:className "group flex justify-between items-center w-full p-5"}
    [:> Flex {:align "center" :gap "5"}
     (when show-checkbox?
       [checkbox])

     [:> Avatar {:size "5"
                 :variant "soft"
                 :radius "medium"
                 :color "gray"
                 :fallback (r/as-element
                            [:> Check {:size 20
                                       :color "gray"}])}]

     [:div {:className "flex flex-col"}
      [:> Text {:size "5" :weight "bold" :className "text-[--gray-12]"} title]
      [:> Text {:size "3" :className "text-[--gray-11]"} subtitle]]]

    [:div {:className "flex space-x-3 items-center"}
     (when show-badge? [badge "Badge"])
     (when show-configure? [configure-button])
     (when show-icon? [status-icon status])
     [:> (.-Trigger Accordion)
      [:> ChevronRight {:size 16 :className "text-[--gray-12] transition-transform duration-300 group-data-[state=open]:rotate-90"}]]]]

   [:> (.-Content Accordion)
    [:> Box {:px "5" :py "7" :className "bg-white border-t border-[--gray-a6] rounded-b-lg"}
     content]]])

(defn main
  "Main component for the Accordion that renders a list of expandable items.

    Parameters:
    - items: A vector of maps, where each map represents an accordion item and should contain the following keys:
      :value        - Unique string to identify the item (required)
      :title        - Title of the item (required)
      :subtitle     - Subtitle of the item (optional)
      :content      - Content to be displayed when the item is expanded (required)
      :status       - Status of the item, affects the displayed icon (optional)
      :show-checkbox? - Boolean, if true, displays a checkbox (optional, default false)
      :show-badge?    - Boolean, if true, displays a badge (optional, default false)
      :show-configure? - Boolean, if true, displays a configure button (optional, default false)
      :show-icon?     - Boolean, if true, displays a status icon (optional, default false)

    Usage example:
    [accordion-root [{:value \"item1\"
                      :title \"Title 1\"
                      :subtitle \"Subtitle 1\"
                      :content \"Content of item 1\"
                      :show-checkbox? true
                      :show-badge? true}
                     {:value \"item2\"
                      :title \"Title 2\"
                      :content \"Content of item 2\"
                      :show-configure? true
                      :show-icon? true}]]"
  [items]
  [:> (.-Root Accordion)
   {:className "w-full"
    :type "single"
    :defaultValue (:value (first items))
    :collapsible true}
   (for [{:keys [value] :as item} items]
     ^{:key value} [accordion-item (merge item {:total-items (count items)})])])
