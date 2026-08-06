(ns webapp.webclient.components.panels.database-schema
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading IconButton Text]]
   ["lucide-react" :refer [Database ChevronsLeft ChevronsRight]]
   [clojure.string :as cs]
   [webapp.components.skip-link :as skip-link]
   [webapp.webclient.components.database-schema :as database-schema]))

(def create-connection-obj
  (memoize
   (fn [connection-name subtype icon_name type]
     {:connection-name connection-name
      :connection-type (cond
                         (not (cs/blank? subtype)) subtype
                         (not (cs/blank? icon_name)) icon_name
                         :else type)})))

(defn main [{:keys [connection collapsed? on-toggle-collapse]}]
  (let [access-disabled? (= (:access_schema connection) "disabled")
        title (if (= "cloudwatch" (:subtype connection))
                "Log Groups"
                "Database Schema")]

    [:> Box {:as "aside"
             ;; gray-2, the shared surface of every chrome panel around the
             ;; editor (log area, metadata panel, output footer). They read as
             ;; one material, and the grey canvas is just the editor.
             :class (str "h-full transition-all duration-300 border-r border-gray-3 bg-gray-2 "
                         (if collapsed? "w-16" "w-full"))
             :aria-label "Database schema"}

     [:> Flex {:align "center"
               :justify "between"
               :class "w-full h-10 p-2 border-b border-gray-3"}
      [:> Flex {:align "center" :gap "2"}
       ;; 14px label and icon. Radix size "2" is 14px on this scale ("3" is
       ;; 16px), so the type stays on the scale rather than an arbitrary class.
       [:> Database {:size 14 :class "text-[--gray-12]" :aria-hidden "true"}]
       [:> Box {:class (when collapsed? "hidden")}
        [:> Heading {:size "2" :weight "bold" :class "text-gray-12"} title]]]
      [:> IconButton {:variant "ghost"
                      :color "gray"
                      :tabIndex "0"
                      :onClick on-toggle-collapse
                      :aria-label (if collapsed?
                                    "Expand database schema panel"
                                    "Collapse database schema panel")
                      :aria-expanded (not collapsed?)}
       [:> (if collapsed? ChevronsRight ChevronsLeft) {:size 16 :aria-hidden "true"}]]]

     (when-not collapsed?
       [:> Box {:p "4" :class "overflow-y-auto h-[calc(100%-40px)]"}
        ;; Skip link: Database Schema → Editor
        [skip-link/main
         {:target-selector "[tabindex='0'][aria-label*='Script editor']"
          :text "Skip to editor"}]

        (if access-disabled?
          [:div {:class "flex flex-col items-center justify-center py-8 text-center"}
           [:> Text {:size "2" :mb "2" :class "text-gray-11"} "Database Schema Disabled"]
           [:> Text {:size "1" :class "text-gray-11"}
            "Schema access is disabled for this connection. Please ask an admin to enable it."]]

          [database-schema/main
           (create-connection-obj
            (:name connection)
            (:subtype connection)
            (:icon_name connection)
            (:type connection))])])]))

