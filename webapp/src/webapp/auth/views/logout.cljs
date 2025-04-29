(ns webapp.auth.views.logout
  (:require [webapp.components.headings :as h]
            [webapp.config :as config]
            [webapp.routes :as routes]
            [re-frame.core :as rf]))

(defn main []
  ;; Remover token primeiro
  (.removeItem js/localStorage "jwt-token")

  ;; Mostrar página de transição e adicionar delay para melhorar experiência
  (js/setTimeout
   #(set! (.. js/window -location -href)
          (str (. (. js/window -location) -origin)
               (routes/url-for :login-hoop)))
   800)  ;; delay de 800ms evita tela piscando

  [:section {:class "antialiased min-h-screen bg-gray-100 flex items-center justify-center"}
   [:div {:class "w-full max-w-md p-8 bg-white rounded-lg shadow-md"}
    [:div {:class "p-regular mb-6 flex justify-center"}
     [:figure {:class "w-36 px-small"}
      [:img {:src (str config/webapp-url "/images/hoop-branding/PNG/hoop-symbol+text_black@4x.png")}]]]

    [:div {:class "text-center mb-6"}
     [h/h2 "Desconectando..." {:class "mb-4"}]
     [:p {:class "text-gray-600"} "Você será redirecionado em instantes."]]

    [:div {:class "flex justify-center"}
     [:div {:class "w-8 h-8 border-t-2 border-b-2 border-blue-500 rounded-full animate-spin"}]]]])
