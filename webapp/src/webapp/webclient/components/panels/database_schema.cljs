(ns webapp.webclient.components.panels.database-schema
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text]]
   [clojure.string :as cs]
   [webapp.webclient.components.database-schema :as database-schema]))

;; Memoized function to create connection object and avoid unnecessary recreations
(def create-connection-obj
  (memoize
   (fn [connection-name subtype icon_name type]
     {:connection-name connection-name
      :connection-type (cond
                         (not (cs/blank? subtype)) subtype
                         (not (cs/blank? icon_name)) icon_name
                         :else type)})))

(defn main [connection]
  (let [access-disabled? (= (:access_schema connection) "disabled")]
    [:> Box {:class "h-full w-full bg-gray-1 border-r border-gray-3 overflow-y-auto"}
     [:> Flex {:justify "between"
               :align "center"
               :class "px-4 py-3 border-b border-gray-3"}
      [:> Text {:size "3" :weight "bold" :class "text-gray-12"}
       (if (= "cloudwatch" (:subtype connection))
         "Log Groups"
         "Database Schema")]]

     [:> Box {:class "p-4"}
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
          (:type connection))])]]))

