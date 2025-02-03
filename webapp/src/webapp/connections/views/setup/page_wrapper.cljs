(ns webapp.connections.views.setup.page-wrapper
  (:require
   ["@radix-ui/themes" :refer [Box]]
   [webapp.connections.views.setup.footer :as footer]))

(defn main [{:keys [children footer-props]}]
  [:> Box {:class "min-h-screen"}
   ;; Main content with padding to account for fixed footer
   [:> Box {:class "pb-6"}
    children]

   ;; Footer with Delete button when in update mode
   (let [base-footer-props (dissoc footer-props :on-delete)]
     [footer/main
      (if (:on-delete footer-props)
        (assoc base-footer-props
               :middle-button {:variant "ghost"
                               :color "red"
                               :text "Delete"
                               :on-click (:on-delete footer-props)})
        base-footer-props)])])
