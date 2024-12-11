(ns webapp.components.accordion
  (:require
   ["@radix-ui/react-accordion" :as Accordion]
   ["@radix-ui/themes" :refer [Box Flex Text Avatar]]
   ["lucide-react" :refer [Check User ChevronRight]]
   [reagent.core :as r]))

(defn status-icon []
  [:> Avatar {:size "1"
              :variant "soft"
              :color "green"
              :radius "full"
              :fallback (r/as-element
                         [:> Check {:size 16
                                    :color "green"}])}])

(defn accordion-item
  [{:keys [title
           subtitle
           content
           disabled
           value
           status
           avatar-icon
           show-icon?]}]
  [:> (.-Item Accordion)
   {:value value
    :disabled disabled
    :className (str "first:rounded-t-6 last:rounded-b-6 data-[state=open]:bg-[--accent-2] "
                    "border-[--gray-a6] data-[disabled]:opacity-70 data-[disabled]:cursor-not-allowed border ")}
   [:> (.-Header Accordion)
    [:> (.-Trigger Accordion) {:className "group flex justify-between items-center w-full p-5"}
     [:> Flex {:align "center" :gap "5"}
      [:> Avatar {:size "5"
                  :variant "soft"
                  :color "gray"
                  :fallback (r/as-element (if avatar-icon
                                            avatar-icon
                                            [:> User {:size 16}]))}]

      [:div {:className "flex flex-col items-start"}
       [:> Text {:size "5" :weight "bold" :className "text-[--gray-12]"} title]
       [:> Text {:size "3" :className "text-[--gray-11] text-"} subtitle]]]

     [:div {:className "flex space-x-3 items-center"}
      (when show-icon? [status-icon status])
      [:> ChevronRight {:size 16
                        :className "text-[--gray-12] transition-transform duration-300 group-data-[state=open]:rotate-90"}]]]]

   [:> (.-Content Accordion)
    [:> Box {:px "5" :py "7" :className "bg-white border-t border-[--gray-a6] rounded-b-6"}
     content]]])

(defn root
  "Main component for the Accordion that renders a list of expandable items.

    Parameters:
    - items: A map containing:
      :value           - Unique string to identify the item (required)
      :title           - Title of the item (required)
      :subtitle        - Subtitle of the item (optional)
      :content         - Content to be displayed when the item is expanded (required)
      :status          - Status of the item, affects the displayed icon (optional)
      :avatar-icon     - Icon to be displayed in the avatar (optional)
      :show-icon?      - Boolean, if true, displays a status icon (optional, default false)

    - id: A unique identifier for the accordion (optional)
    - initial-open?: Boolean, if true, the first item will be expanded by default (optional)
    - open?: Value to trigger opening/closing of a specific accordion item
    - on-change: Callback function to be called when an item is opened/closed

    Usage example:
   [accordion/root
    {:item {:value \"item1\"
            :title \"Title 1\"
            :subtitle \"Subtitle 1\"
            :content \"Content\"}
     :id \"my-accordion\"
     :first-open? true}]"

  [{:keys [item id open? on-change]}]
  [:> (.-Root Accordion)
   {:className "w-full"
    :id id
    :value (when open? (:value item))
    :onValueChange #(on-change (not= % ""))
    :type "single"
    :collapsible true}
   [accordion-item item]])
