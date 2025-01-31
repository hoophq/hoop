(ns webapp.connections.views.setup.headers
  (:require
   ["@radix-ui/themes" :refer [Box Heading Text]]
   [webapp.config :as config]
   [webapp.connections.views.setup.stepper :as stepper]))

(defn setup-header []
  [:> Box {:class "space-y-6"}
   [:> Box
    [:img {:src (str config/webapp-url "/images/hoop-branding/PNG/hoop-symbol_black@4x.png")
           :class "w-16 mx-auto py-4"}]]
   [:> Box
    [:> Heading {:as "h1" :align "center" :size "6" :mb "2" :weight "bold" :class "text-[--gray-12]"}
     "Setup a new connection"]
    [:> Text {:as "p" :size "3" :align "center" :class "text-[--gray-12]"}
     "Follow the steps to setup a connection to your resources."]]

   [stepper/main]])

(defn console-all-done-header []
  [:> Box {:class "space-y-6"}
   [:> Box
    [:img {:src (str config/webapp-url "/images/hoop-branding/PNG/hoop-symbol_black@4x.png")
           :class "w-16 mx-auto py-4"}]]
   [:> Box
    [:> Heading {:as "h1" :align "center" :size "6" :mb "2" :weight "bold" :class "text-[--gray-12]"}
     "All done, just one more step"]
    [:> Text {:as "p" :size "3" :align "center" :class "text-[--gray-12]"}
     "Follow the instructions to install and run hoop.dev in your application."]]])
