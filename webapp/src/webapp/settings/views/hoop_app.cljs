(ns webapp.settings.views.hoop-app
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.forms :as forms]
            [webapp.components.headings :as h]))


(defn- hoop-app-configuration [api-url grpc-url]
  [:section
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
                     :placeholder "https://app.hoop.dev"
                     :value @api-url}]
       [forms/input {:label "gRPC URL"
                     :on-change #(reset! grpc-url (-> % .-target .-value))
                     :classes "whitespace-pre overflow-x"
                     :placeholder "app.hoop.dev:8443"
                     :value @grpc-url}]]
      [:div {:class "grid grid-cols-3 justify-items-end"}
       [:div {:class "col-end-4 w-full"}
        (button/primary {:text "Save"
                         :type "submit"
                         :full-width true})]]]]]])

(defn main []
  (let [hoop-configs (rf/subscribe [:hoop-app->my-configs])
        api-url (r/atom (or (:apiUrl @hoop-configs) ""))
        grpc-url (r/atom (or (:grpcUrl @hoop-configs) ""))]
    (rf/dispatch [:hoop-app->get-my-configs])
    [hoop-app-configuration api-url grpc-url]))
