(ns webapp.components.snackbar
  (:require
   [re-frame.core :as rf]
   [webapp.config :as config]))

(defmulti level-icon identity)
(defmethod level-icon :error [_] (str config/webapp-url "/icons/icon-important-red.svg"))
(defmethod level-icon :success [_] (str config/webapp-url "/icons/icon-check-green.svg"))
(defmethod level-icon :info [_] (str config/webapp-url "/icons/icon-information-white.svg"))
(defmethod level-icon :default [_] (str config/webapp-url "/icons/icon-information-white.svg"))

(defmulti markup identity)
(defmethod markup :shown [_ state]
  (js/setTimeout #(rf/dispatch [:hide-snackbar]) 10000)
  ;; Above the legacy overlay band — modal, dialog and the command palette are
  ;; all z-[201] and Radix poppers 202 — so a toast fired from inside a modal is
  ;; still readable. The top offset clears the React shell's global header the
  ;; same way the sonner Toaster beside it does (see toaster-offset in app.cljs),
  ;; and collapses back to the original 2rem when the shell is not mounted.
  [:div {:class (str "flex align-center z-[203] fixed max-w-xs right-8 "
                     "top-[calc(var(--app-shell-header-offset,0rem)_+_2rem)] "
                     "p-regular bg-gray-800 "
                     "font-light text-gray-100 leading-5 rounded-lg shadow-lg animate-appear-right whitespace-normal")}
   [:figure {:class "flex-shrink-0 w-6 mr-regular"}
    [:img {:src (level-icon (:level state))}]]
   [:div.flex-shrink {:class "overflow-auto"}
    [:small {:class "whitespace-normal"}
     (:text state)]]
   [:figure.flex-shrink-0.w-6.ml-regular.cursor-pointer
    {:on-click #(rf/dispatch [:hide-snackbar])}
    [:img {:src (str config/webapp-url "/icons/icon-close-white.svg")}]]])

(defmethod markup :default [_] nil)

(defn snackbar []
  (let [state @(rf/subscribe [:snackbar])]
    (markup (:status state) state)))
