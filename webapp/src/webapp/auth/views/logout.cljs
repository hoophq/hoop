(ns webapp.auth.views.logout
  (:require
   ["@radix-ui/themes" :refer [Spinner]]
   [webapp.components.headings :as h]
   [webapp.config :as config]
   [webapp.routes :as routes]))

(defn main []
  (.removeItem js/localStorage "jwt-token")

  (js/setTimeout
   #(set! (.. js/window -location -href)
          (str (. (. js/window -location) -origin)
               (routes/url-for :login-hoop)))
   800)

  [:section {:class "antialiased min-h-screen bg-gray-100 flex items-center justify-center"}
   [:div {:class "w-full max-w-md p-8 bg-white rounded-lg shadow-md"}
    [:div {:class "p-regular mb-6 flex justify-center"}
     [:figure {:class "w-36 px-small"}
      [:img {:src (str config/webapp-url "/images/hoop-branding/PNG/hoop-symbol+text_black@4x.png")}]]]

    [:div {:class "text-center mb-6"}
     [h/h2 "Logging out..." {:class "mb-4"}]
     [:p {:class "text-gray-600"} "You will be redirected in a few seconds."]]

    [:div {:class "flex justify-center"}
     [:> Spinner {:size "3"}]]]])
