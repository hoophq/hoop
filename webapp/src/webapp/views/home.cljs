(ns webapp.views.home
  (:require [re-frame.core :as rf]))

(defn home-panel-hoop [_]
  (fn []
    (rf/dispatch [:navigate :editor-plugin])))
