(ns webapp.components.theme-provider
  (:require
   ["@radix-ui/themes" :refer [Theme]]))

(defn theme-provider [& children]
  [:> Theme {:radius "large" :panelBackground "solid"}
   (into [:<>] children)])
