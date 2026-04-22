(ns webapp.components.loaders
  (:require
   [re-frame.core :as rf]
   [webapp.subs :as subs]))

(defn- dots-loader []
  [:div {:class "flex gap-1.5"}
   [:div {:class "w-2 h-2 rounded-full bg-primary-9 animate-bounce"
          :style {:animation-delay "0ms"}}]
   [:div {:class "w-2 h-2 rounded-full bg-primary-9 animate-bounce"
          :style {:animation-delay "150ms"}}]
   [:div {:class "w-2 h-2 rounded-full bg-primary-9 animate-bounce"
          :style {:animation-delay "300ms"}}]])

(def spinner-lg
  [:div.w-12.h-12.rounded-full.border-8.border-t-gray-600.animate-spin])

(defmulti page-loader identity)
(defmethod page-loader :default [_ _] nil)
(defmethod page-loader :open [_ fadeout]
  [:div {:class (str "fixed flex flex-col items-center justify-center gap-6 inset-0 w-full h-full z-30 bg-white transition " fadeout)}
   [:span {:class "text-[22px] font-bold tracking-tight text-primary-9 leading-none"}
    "hoop"]
   [dots-loader]])
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

(defn page-loading-screen
  "Clean, centered full-page loading screen. No card/box wrapping.
   Mirrors the React PageLoader component for visual consistency."
  [{:keys [message description]}]
  [:div {:class "min-h-screen flex flex-col items-center justify-center gap-6"}
   [:span {:class "text-[22px] font-bold tracking-tight text-primary-9 leading-none"}
    "hoop"]
   [dots-loader]
   (when (or message description)
     [:div {:class "flex flex-col items-center gap-1 text-center"}
      (when message
        [:p {:class "text-sm font-medium text-gray-11"} message])
      (when description
        [:p {:class "text-xs text-gray-11 opacity-70"} description])])])
