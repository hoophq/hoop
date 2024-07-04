(ns webapp.components.badge
  (:require
    [webapp.components.icon :as icon]))

(defn select
  "Element from: https://tailwindui.com/components/application-ui/elements/badges
  text -> the actual html you need inside the badge
  on-close -> callback that will be triggered when user clicks on close"
  [{:keys [text on-click selected?]}]
  [:span
   {:class (str "flex items-center gap-0.5 "
                "inline-flex items-center border border-transparent py-1.5 px-3 text-xs "
                "bg-blue-50 text-blue-600 font-medium rounded-full cursor-pointer "
                "transition hover:border-blue-500 "
                (when selected? "border-blue-500"))
    :on-click #(on-click {:selected? selected?
                          :text text})}
   [:span text]
   (when selected?
     [:span
      [icon/regular {:icon-name "check-blue"
                     :size 4}]])])

(defn small
  "Element from: https://tailwindui.com/components/application-ui/elements/badges
  text -> the actual html you need inside the badge
  on-close -> callback that will be triggered when user clicks on close"
  [text on-close]
  [:span
   {:class "inline-flex items-center py-0.5 pl-2 pr-0.5 rounded-full text-xs font-medium bg-gray-200 text-gray-700"}
   text
   [:button
    {:class "flex-shrink-0 ml-0.5 h-4 w-4 rounded-full inline-flex items-center justify-center text-gray-400 hover:bg-gray-300 hover:text-gray-500 focus:outline-none focus:bg-gray-300 focus:text-gray-500"
     :on-click on-close}

    [:span.sr-only (str "Remove " text)]
    [:svg {:class "h-2 w-2"
           :stroke "currentColor"
           :fill "none"
           :viewBox "0 0 8 8"}
     [:path {:strokeLinecap "round"
             :strokeWidth "1.5"
             :d "M1 1l6 6m0-6L1 7"}]]]])

(defn basic
  "Element from: https://tailwindui.com/components/application-ui/elements/badges
  text -> the actual html you need inside the badge"
  [text]
  [:span
   {:class "py-0.5 px-2 rounded-full text-xs font-medium bg-gray-200 text-gray-700"}
   text])
