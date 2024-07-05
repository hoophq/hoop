(ns webapp.components.loaders
  (:require
   [re-frame.core :as rf]
   [webapp.subs :as subs]))

(def spinner-lg
  [:div.w-12.h-12.rounded-full.border-8.border-t-gray-600.animate-spin])

(defmulti page-loader identity)
(defmethod page-loader :default [_ _] nil)
(defmethod page-loader :open [_ fadeout]
  [:div.fixed.flex.items-center.place-content-center.inset-0.w-full.h-full.z-30.bg-white.transition.text-gray-200
   {:class fadeout}
   spinner-lg])
(defmethod page-loader :closing [_ _]
  (js/setTimeout #(rf/dispatch [:destroy-page-loader]) 200)
  (page-loader :open "opacity-0"))

(defn over-page-loader []
  (let [page-loader-status @(rf/subscribe [::subs/page-loader])]
    (page-loader (:status page-loader-status) "")))

(defn simple-loader [{:keys [size border-size]}]
  [:div {:class (str "rounded-full border-t-gray-400 animate-spin"
                     (if border-size
                       (str " border-" border-size)
                       " border-4")
                     (if size
                       (str " w-" size " h-" size)
                       " w-6 h-6"))}])
