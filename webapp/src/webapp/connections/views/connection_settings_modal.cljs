(ns webapp.connections.views.connection-settings-modal
  (:require ["@radix-ui/themes" :refer [Box Heading Text]]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.forms :as forms]))

(def default-port "8999")
(def default-duration "30")

;; Duration in minutes to nanoseconds conversion
(defn minutes-to-ns [minutes]
  (* minutes 60 1000 1000 1000))

(defn main [connection-name]
  (let [port (r/atom default-port)
        duration (r/atom default-duration)]
    (fn []
      [:> Box
       [:header {:class "mb-4"}
        [:> Heading {:size "6" :as "h2"}
         "Hoop Access Settings"]]

       [:main {:class "space-y-5"}
        [:div
         [:> Text {:as "label" :size "2" :weight "bold"}
          "Connection name"]
         [:> Text {:as "div" :size "2" :class "text-gray-600"}
          connection-name]]

        [:div
         [forms/input {:label "Port"
                       :placeholder "Port for connection"
                       :value @port
                       :on-change #(reset! port (.. % -target -value))
                       :size "2"}]]

        [:div
         [forms/input {:label "Duration (minutes)"
                       :placeholder "Enter minutes (e.g. 30)"
                       :value @duration
                       :type "number"
                       :min "5"
                       :max "1440" ;; 24 hours
                       :on-change #(reset! duration (.. % -target -value))
                       :size "2"}]
         [:div {:class "text-xs text-gray-500 mt-1"}
          "Minimum: 5 minutes, Maximum: 24 hours (1440 minutes)"]]]

       [:footer {:class "mt-6 flex justify-end gap-3"}
        [button/secondary {:text "Cancel"
                           :outlined true
                           :on-click #(rf/dispatch [:modal->close])}]
        [button/primary {:text "Connect"
                         :on-click #(do
                                      (rf/dispatch [:modal->close])
                                      (rf/dispatch [:connections->start-connect-with-settings
                                                    {:connection-name connection-name
                                                     :port @port
                                                     :access-duration (minutes-to-ns (js/parseInt @duration))}]))}]]])))
