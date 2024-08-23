(ns webapp.hoop-app.main
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.divider :as divider]
            [webapp.components.forms :as forms]
            [webapp.components.headings :as h]))


(defn- hoop-app-configuration [api-url grpc-url]
  [:div {:class "grid grid-cols-3 gap-large my-large"}
   [:div {:class "col-span-1"}
    [h/h3 "Hoop app configuration" {:class "text-gray-800"}]
    [:span {:class "block text-sm mb-regular text-gray-600"}
     "Here you will be able to change the configs of your hoop app"]]
   [:div {:class "col-span-2"}
    [:form
     {:class "mb-regular"
      :on-submit (fn [e]
                   (.preventDefault e)
                   (rf/dispatch [:hoop-app->update-my-configs {:apiUrl @api-url
                                                               :grpcUrl @grpc-url}]))}
     [:div {:class "grid gap-regular"}
      [forms/input {:label "API URL"
                    :on-change #(reset! api-url (-> % .-target .-value))
                    :classes "whitespace-pre overflow-x"
                    :placeholder "https://use.hoop.dev"
                    :value @api-url}]
      [forms/input {:label "gRPC URL"
                    :on-change #(reset! grpc-url (-> % .-target .-value))
                    :classes "whitespace-pre overflow-x"
                    :placeholder "use.hoop.dev:8443"
                    :value @grpc-url}]]
     [:div {:class "grid grid-cols-3 justify-items-end"}
      [:div {:class "col-end-4 w-full"}
       (button/primary {:text "Save"
                        :type "submit"
                        :full-width true})]]]]])
(def pooling-get-app-status
  (fn [interval]
    (rf/dispatch [:hoop-app->get-app-status])
    (js/setTimeout #(pooling-get-app-status interval) interval)))

(defn main []
  (let [hoop-configs (rf/subscribe [:hoop-app->my-configs])
        app-running? (rf/subscribe [:hoop-app->running?])
        api-url (r/atom (or (:apiUrl @hoop-configs) ""))
        grpc-url (r/atom (or (:grpcUrl @hoop-configs) ""))]
    (rf/dispatch [:hoop-app->get-my-configs])
    (pooling-get-app-status 5000)
    (fn []
      [:section {:class "bg-white rounded-lg h-full p-6 overflow-y-auto"}
       (if (not @app-running?)
         [:div {:class "bg-white h-full px-large py-regular text-center"}
          [:div {:class "pb-regular"}
           [h/h3 "Hoop app not running" {:class "text-gray-800"}]
           [:span {:class "block text-sm mb-regular text-gray-600"}
            "You need to run the hoop app to be able to use the features of this page"]]
          [divider/labeled "or"]
          [:div {:class "pt-large"}
           [:a {:href "https://install.hoop.dev"
                :class (str "rounded-md leading-6 text-xs px-6 py-3 "
                            "text-white text-sm font-semibold bg-blue-500 hover:bg-blue-600 ")}
            "Download Hoop App"]]]

         [hoop-app-configuration api-url grpc-url])])))
