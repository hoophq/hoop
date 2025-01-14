(ns webapp.connections.views.setup.headers
  (:require
   ["@radix-ui/themes" :refer [Box Heading Text]]))

(defn setup-header []
  [:> Box {:class "mb-8"}
   [:> Heading {:as "h1" :align "center" :size "6" :mb "2" :weight "bold" :class "text-[--gray-12]"}
    "Setup a new connection"]
   [:> Text {:as "p" :size "3" :align "center" :class "text-[--gray-12]"}
    "Follow the steps to setup a connection to your resources."]])

(defn console-all-done-header []
  [:> Box {:class "mb-8"}
   [:> Heading {:as "h1" :align "center" :size "6" :mb "2" :weight "bold" :class "text-[--gray-12]"}
    "All done, just one more step"]
   [:> Text {:as "p" :size "3" :align "center" :class "text-[--gray-12]"}
    "Follow the instructions to install and run hoop.dev in your application."]])
