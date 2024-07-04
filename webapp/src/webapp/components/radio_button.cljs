(ns webapp.components.radio-button
  (:require ["@headlessui/react" :as ui]
            [reagent.core :as r]))

(defn main
  "Radio button component

   parameters:
   HashMap {
     :label (string) -> the label which will show up by the side of the radio button;
     :name (string) -> the name to distinguish from other radio button;
     :value (string) -> value from radio button;
     :on-change (function) -> function which will dispatch when select the radio button;
     :checked? (boolean) -> boolean to start the radio button checked or not;
   }"
  [{:keys [label name value on-change options]}]
  [:div {:class "w-full"}
   [:> ui/RadioGroup {:value value
                      :onChange on-change
                      :name name}
    [:> (.-Label ui/RadioGroup) {:className "text-sm font-semibold text-gray-700"}
     label]
    (for [option options]
      [:> (.-Option ui/RadioGroup)
       {:key (:value option)
        :value (:value option)
        :className (fn [params]
                     (str "relative flex cursor-pointer flex-col py-2 focus:outline-none "
                          (if (.-checked params)
                            "z-10"
                            "border-gray-200")))}
       (fn [params]
         (r/as-element
          [:<>
           [:span {:class "flex items-center text-sm"}
            [:span {:aria-hidden "true"
                    :class (str "h-4 w-4 rounded-full border flex items-center justify-center "
                                (if (.-checked params)
                                  "border-transparent bg-gray-900"
                                  "border-gray-300")
                                (when (.-active params)
                                  "ring-2 ring-offset-2 ring-indigo-600 bg-gray-900 "))}
             [:span {:class (str "rounded-full w-1.5 h-1.5 "
                                 (if (.-checked params)
                                   "bg-white"
                                   "bg-transparent"))}]]
            [:> (.-Label ui/RadioGroup) {:as "span"
                                         :className (str "ml-3 font-medium")}
             (:text option)]]]))])]])
