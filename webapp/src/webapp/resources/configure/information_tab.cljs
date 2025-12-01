(ns webapp.resources.configure.information-tab
  (:require
   ["@radix-ui/themes" :refer [Box Flex Grid Heading Text TextField]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [webapp.connections.constants :as conn-constants]))

(defn main [resource new-name-atom]
  (let [icon-url (conn-constants/get-connection-icon {:type (:type resource)
                                                      :subtype (:subtype resource)}
                                                     "default")
        updating? (rf/subscribe [:resources->updating?])]
    (fn []
      [:> Box {:class "space-y-16"}
       ;; Resource type
       [:> Grid {:columns "7" :gap "7"}
        [:> Box {:grid-column "span 3 / span 3"}
         [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
          "Resource type"]
         [:> Text {:size "2" :class "text-[--gray-11]"}
          "This name is used to identify your Agent in your environment."]]

        [:> Flex {:grid-column "span 4 / span 4" :direction "column" :justify "between"
                  :class "h-[110px] p-radix-4 rounded-lg border border-gray-3 bg-white"}

         [:> Flex {:gap "3" :align "center" :justify "between"}
          (when icon-url
            [:img {:src icon-url
                   :class "w-6 h-6"
                   :alt (or (:subtype resource) "resource")}])]

         [:> Box
          [:> Text {:size "3" :weight "bold" :class "text-[--gray-12]"}
           (cs/capitalize (:type resource))]]]]

       ;; Resource Name
       [:> Grid {:columns "7" :gap "7"}
        [:> Box {:grid-column "span 3 / span 3"}
         [:> Heading {:as "h3" :size "4" :weight "bold" :class "text-[--gray-12]"}
          "Resource ID"]
         [:> Text {:size "2" :class "text-[--gray-11]"}
          "Used to identify this Resource in your environment."]]

        [:> Box {:grid-column "span 4 / span 4"}
         [:> Box {:class "flex-1"}
          [:> Box {:class "space-y-1"}
           [:> Text {:as "label" :size "2" :weight "medium" :class "text-[--gray-12]"}
            "Name"]
           [:> TextField.Root
            {:value @new-name-atom
             :onChange #(let [value (-> % .-target .-value)
                              ;; Replace spaces with hyphens automatically
                              sanitized-value (cs/replace value #"\s+" "-")]
                          (reset! new-name-atom sanitized-value))
             :disabled @updating?
             :placeholder "Enter resource name"}]]]]]])))
