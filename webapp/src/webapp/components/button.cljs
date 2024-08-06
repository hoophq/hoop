(ns webapp.components.button
  (:require
   [reagent.core :as r]
   [webapp.components.icon :as icon]
   [webapp.components.popover :as popover]))

(def common-classes
  "disabled:opacity-50 disabled:cursor-not-allowed
   leading-none transition
   font-semibold text-sm")
(defmulti btn-variant identity)
(defmethod btn-variant :small [] "h-8 text-xs")
(defmethod btn-variant :default [] "h-12")

(defmulti green identity)
(defmethod green :small [_ {:keys [text on-click type disabled]}]
  [:button.py-small.px-regular.text-green-50.bg-green-500.hover:bg-green-600
   {:on-click on-click
    :class common-classes
    :disabled disabled
    :type (or type "button")}
   text])

(defmethod green :rounded [_ {:keys [text on-click type disabled]}]
  [:button.rounded-full.py-x-small.px-x-small.text-green-50.bg-green-500.hover:bg-green-600
   {:on-click on-click
    :class common-classes
    :disabled disabled
    :type (or type "button")}
   text])

(defmethod green :default [_ {:keys [text on-click type disabled]}]
  [:button.py-regular.px-large.text-green-50.bg-green-500.hover:bg-green-600
   {:on-click on-click
    :class common-classes
    :type (or type "button")
    :disabled disabled}
   text])

(defmulti red identity)
(defmethod red :small-transparent [_ {:keys [text on-click type disabled]}]
  [:button.py-small.px-small.text-red-500.bg-transparent.hover:bg-red-50
   {:on-click on-click
    :class common-classes
    :disabled disabled
    :type (or type "button")}
   text])

(defmethod red :rounded-transparent [_ {:keys [text on-click type disabled]}]
  [:button.rounded-full.py-x-small.px-x-small.text-red-500.bg-transparent.hover:bg-red-50
   {:on-click on-click
    :class common-classes
    :disabled disabled
    :type (or type "button")}
   text])

(defmulti btn-status identity)
(defmethod btn-status :loading [_ _]
  [:figure.block.flex.place-content-center.justify-center
   [:img.w-4.animate-spin {:src "/icons/icon-loader-circle-white.svg"}]])
(defmethod btn-status :default [_ text] text)

(defn- dropdown-more-options [options cb]
  [:ul {:class "w-56 max-h-80 text-center overflow-y-auto"}
   (for [option options]
     ^{:key option}
     [:li {:on-click #(cb option)
           :class (str "p-regular border-b cursor-pointer "
                       "text-gray-800 font-normal hover:bg-gray-50")}
      option])])

(defn- button-base [_]
  (let [more-options-open? (r/atom false)]
    (fn [{:keys [text icon on-click type disabled status classes variant full-width more-options on-click-option]}]
      (let [has-more-options? (> (count more-options) 0)]
        [:div {:class (str "flex justify-self-end flex-shrink-0 " (when full-width "w-full"))}
         [:button
          {:on-click on-click
           :class (str "flex flex-grow items-center justify-center "
                       (when icon " pr-small ")
                       common-classes " " (btn-variant variant) " " classes
                       (if has-more-options? " rounded-l-lg" " rounded-lg")
                       (when full-width " w-full")
                       (when disabled " cursor-not-allowed"))
           :type (or type "button")
           :disabled (or disabled (= status :loading))}
          [:span
           {:class (str "pl-large"
                        (when (not icon) " pr-large")
                        (when (= variant :small) " text-xs"))}
           [btn-status status text]]
          (when icon
            [icon/regular {:size 6
                           :icon-name icon}])]
         (when has-more-options?
           [:span.relative.flex.cursor-pointer
            {:class (str common-classes " " (btn-variant variant)
                         " px-small rounded-r-lg border-l border-blue-400 "
                         classes)
             :on-click #(reset! more-options-open? (not @more-options-open?))}
            [:img.inline.w-5 {:src "/icons/icon-cheveron-down-white.svg"}]
            [popover/top {:open @more-options-open?
                          :component [dropdown-more-options more-options on-click-option]}]])]))))

(defn red-new
  [{:keys [text on-click type disabled status size variant full-width more-options on-click-option]}]
  [button-base {:on-click on-click
                :text text
                :type type
                :disabled disabled
                :full-width full-width
                :variant (or size :default)
                :status status
                :more-options more-options
                :on-click-option on-click-option
                :classes (if (= variant :outline)
                           (str "text-red-500 bg-gray-50 hover:bg-gray-100"
                                " border border-gray-200 hover:border-gray-300")

                           (str "text-gray-50 bg-red-600 hover:bg-red-700"
                                " hover:shadow-red-button-hover"))}])

(defn black
  [{:keys [text icon on-click type disabled status variant full-width more-options on-click-option]}]
  [button-base {:on-click on-click
                :text text
                :icon icon
                :type type
                :disabled disabled
                :full-width full-width
                :variant (or variant :default)
                :status status
                :more-options more-options
                :on-click-option on-click-option
                :classes (str "text-gray-50 bg-gray-900"
                              " hover:shadow-black-button-hover")}])

(defn primary
  [{:keys [text icon on-click type disabled status variant full-width more-options on-click-option]}]
  [button-base {:on-click on-click
                :text text
                :icon icon
                :type type
                :disabled disabled
                :full-width full-width
                :variant (or variant :default)
                :status status
                :more-options more-options
                :on-click-option on-click-option
                :classes (str "text-blue-50 bg-blue-500 hover:bg-blue-600"
                              " hover:shadow-blue-button-hover")}])

(defn secondary
  [{:keys [text on-click type disabled variant status outlined full-width]}]
  [button-base {:on-click on-click
                :text text
                :type (or type "button")
                :disabled disabled
                :full-width full-width
                :variant (or variant :default)
                :status status
                :classes (str "text-gray-900 bg-transparent hover:bg-gray-100"
                              " hover:shadow-secondary-button-hover"
                              (when outlined " border border-gray-300 "))}])

(defn tailwind-primary [{:keys [text on-click type disabled full-width classes]}]
  [:button {:on-click on-click
            :type (or type "button")
            :disabled disabled
            :class (str classes " "
                        (when full-width "w-full ")
                        "rounded-md leading-6 text-xs px-3.5 py-1.5 "
                        "text-white font-semibold bg-blue-500 hover:bg-blue-600 "
                        (when disabled "cursor-not-allowed opacity-70"))}
   text])

(defn tailwind-secondary [{:keys [text on-click type disabled full-width outlined? dark?]}]
  [:button {:on-click on-click
            :type (or type "button")
            :disabled disabled
            :class (str (when full-width "w-full ")
                        "rounded-md leading-6 px-3.5 py-1.5 text-xs font-semibold "
                        "shadow-sm hover:bg-white hover:bg-opacity-20"
                        (when outlined? " border border-gray-300 text-gray-800")
                        (when dark? " text-white "))}
   text])


(defn tailwind-tertiary [{:keys [text on-click type disabled full-width]}]
  [:button {:on-click on-click
            :type (or type "button")
            :disabled disabled
            :class (str (when full-width "w-full ")
                        "rounded-md leading-6 px-3.5 py-1.5 text-xs font-semibold "
                        "shadow-sm text-gray-800 bg-gray-100 hover:bg-opacity-20")}
   text])
