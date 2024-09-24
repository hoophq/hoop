(ns webapp.connections.views.select-connection-use-cases
  (:require [re-frame.core :as rf]
            [webapp.components.headings :as h]))

(defn main []
  (rf/dispatch [:agents->get-agents])
  (fn []
    [:main {:class "lg:h-full bg-white flex lg:justify-center items-center flex-col gap-small px-4 lg:px-24 pb-64 pt-16"}
     [:header
      [:h1 {:class "mb-8 text-2xl font-bold text-gray-900"}
       "What do you want to connect?"]]
     [:section {:class "grid lg:grid-cols-2 gap-regular"}
      [:div {:class "flex flex-col items-center border rounded-lg cursor-pointer hover:shadow p-4"
             :on-click #(rf/dispatch
                         [:navigate
                          :create-hoop-connection
                          {}
                          :type "database"])}
       [:figure {:class "w-full rounded-lg border mb-2"}
        [:img {:class "w-full h-28 p-3"
               :src "/images/database-connections.svg"}]]
       [:div {:class "flex flex-col justify-center items-center"}
        [h/h4-md "Database"]]]
      [:div {:class "col-span-2 flex items-center border rounded-lg cursor-pointer hover:shadow p-4"
             :on-click #(rf/dispatch
                         [:navigate
                          :create-hoop-connection
                          {}
                          :type "custom"])}
       [:figure {:class "rounded-lg border"}
        [:img {:class "w-full p-3"
               :src "/images/custom-connections-small.svg"}]]
       [:div {:class "flex flex-col pl-3 justify-center"}
        [h/h4-md "Custom"]
        [:span {:class "mt-2 text-sm text-center text-gray-500"}
         "Advanced setup, ideal for some specific uses"]]]
      [:div {:class "col-span-2 flex items-center border rounded-lg cursor-pointer hover:shadow p-4"
             :on-click #(rf/dispatch
                         [:navigate
                          :create-hoop-connection
                          {}
                          :type "custom"])}
       [:figure {:class "rounded-lg border"}
        [:img {:class "w-full p-3"
               :src "/images/custom-connections-small.svg"}]]
       [:div {:class "flex flex-col pl-3 justify-center"}
        [h/h4-md "Shell"]
        [:span {:class "mt-2 text-sm text-center text-gray-500"}
         "Advanced setup, ideal for some specific uses"]]]
      [:div {:class "col-span-2 flex items-center border rounded-lg cursor-pointer hover:shadow p-4"
             :on-click #(rf/dispatch
                         [:navigate
                          :create-hoop-connection
                          {}
                          :type "custom"])}
       [:figure {:class "rounded-lg border"}
        [:img {:class "w-full p-3"
               :src "/images/custom-connections-small.svg"}]]
       [:div {:class "flex flex-col pl-3 justify-center"}
        [h/h4-md "TCP"]
        [:span {:class "mt-2 text-sm text-center text-gray-500"}
         "Advanced setup, ideal for some specific uses"]]]
      [:div {:class "flex flex-col items-center border rounded-lg cursor-pointer hover:shadow p-4"
             :on-click #(rf/dispatch
                         [:navigate
                          :create-hoop-connection
                          {}
                          :type "application"])}
       [:figure {:class "w-full rounded-lg border mb-2"}
        [:img {:class "w-full h-28 p-3"
               :src "/images/application-connections.svg"}]]
       [:div {:class "flex flex-col justify-center items-center"}
        [h/h4-md "Application"]]]]]))

