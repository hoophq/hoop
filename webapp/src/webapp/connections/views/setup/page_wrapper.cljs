(ns webapp.connections.views.setup.page-wrapper
  (:require
   ["@radix-ui/themes" :refer [Box]]
   [webapp.connections.views.setup.footer :as footer]))

(defn main [{:keys [children footer-props]}]
  [:> Box {:class "min-h-screen bg-gray-1"}
   ;; Main content with padding to account for fixed footer
   [:> Box {:class "pb-6"}
    children]

   ;; Footer
   [footer/main footer-props]])
