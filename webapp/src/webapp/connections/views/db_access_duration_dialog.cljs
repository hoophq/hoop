(ns webapp.connections.views.db-access-duration-dialog
  (:require
   ["@radix-ui/themes" :refer [Button Heading Text Box]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.connections.constants.db-access :as db-access-constants]))

(defn main [connection]
  (let [selected-duration (r/atom 30)
        requesting? (rf/subscribe [:db-access->requesting?])]

    (fn [_connection]
      [:> Box {:class "space-y-8"}
       [:header {:class "mb-4"}
        [:> Heading {:size "6" :as "h2"}
         "Configure session"]
        [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
         "Specify how long you need access to this connection."]]

       [:> Box {:class "space-y-3"}
        [:> Box
         [:> Text {:as "label" :size "2" :weight "bold" :class "text-[--gray-12]"}
          "Access duration"]
         [forms/select
          {:size "2"
           :not-margin-bottom? true
           :placeholder "Select duration"
           :on-change #(reset! selected-duration (js/parseInt %))
           :selected @selected-duration
           :full-width? true
           :options db-access-constants/access-duration-options}]]

        [:> Text {:as "p" :size "2" :class "text-[--gray-11]"}
         "Your access will automatically expire after this period"]]

       [:footer {:class "flex justify-end gap-3 mt-6"}
        [:> Button
         {:variant "solid"
          :loading @requesting?
          :disabled @requesting?
          :on-click #(rf/dispatch [:db-access->request-access
                                   (:name connection)
                                   @selected-duration])}
         "Confirm and Connect"]]])))
