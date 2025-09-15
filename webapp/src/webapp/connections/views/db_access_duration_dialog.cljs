(ns webapp.connections.views.db-access-duration-dialog
  (:require
   ["@radix-ui/themes" :refer [Button Heading]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.connections.constants.db-access :as db-access-constants]))

(defn main [connection]
  (let [selected-duration (r/atom db-access-constants/default-access-duration)
        requesting? (rf/subscribe [:db-access->requesting?])]

    (fn [_connection]
      [:section
       [:header {:class "mb-4"}
        [:> Heading {:size "6" :as "h2"}
         "Configure session"]]

       [:main {:class "space-y-4"}
        [:p {:class "text-sm text-gray-600"}
         "Specify how long you need access to this connection."]

        [:div
         [:label {:class "block text-sm font-medium text-gray-700 mb-2"}
          "Access duration"]
         [forms/select
          {:size "2"
           :not-margin-bottom? true
           :placeholder "Select duration"
           :on-change #(reset! selected-duration (js/parseInt %))
           :selected (str @selected-duration)
           :full-width? true
           :options db-access-constants/access-duration-options}]]

        [:p {:class "text-sm text-gray-500"}
         "Your access will automatically expire after this period"]]

       [:footer {:class "flex justify-end gap-3 mt-6"}
        [:> Button
         {:variant "outline"
          :disabled @requesting?
          :on-click #(rf/dispatch [:modal->close])}
         "Cancel"]

        [:> Button
         {:variant "solid"
          :loading @requesting?
          :disabled @requesting?
          :on-click #(rf/dispatch [:db-access->request-access
                                   (:name connection)
                                   @selected-duration])}
         (if @requesting? "Connecting..." "Confirm and Connect")]]])))
