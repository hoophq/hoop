(ns webapp.webclient.components.panels.database-schema
  (:require
   ["@radix-ui/themes" :refer [Box Flex Heading IconButton Text]]
   ["lucide-react" :refer [Database ChevronsLeft ChevronsRight]]
   [clojure.string :as cs]
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
             :class (str "h-full transition-all duration-300 border-r-2 border-gray-3 bg-gray-1 "
                         (if collapsed? "w-16" "w-full"))}

     [:> Flex {:align "center"
               :justify "between"
               :class "w-full h-10 p-2 border-b border-gray-3"}
      [:> Flex {:align "center" :gap "2"}
       [:> Database {:size 16 :class "text-[--gray-12]"}]
       [:> Box {:class (when collapsed? "hidden")}
        [:> Heading {:size "3" :weight "bold" :class "text-gray-12"} title]]]
      [:> IconButton {:variant "ghost"
                      :color "gray"
                      :onClick on-toggle-collapse}
       [:> (if collapsed? ChevronsRight ChevronsLeft) {:size 16}]]]

     (when-not collapsed?
       [:> Box {:p "4" :class "overflow-y-auto h-[calc(100%-40px)]"}
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

