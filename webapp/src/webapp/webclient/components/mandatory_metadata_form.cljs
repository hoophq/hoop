(ns webapp.webclient.components.mandatory-metadata-form
  (:require
   ["@radix-ui/themes" :refer [Box Button Flex Heading Text]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]))

(defn- field-input [{:keys [label value-atom]}]
  [forms/input
   {:label label
    :required true
    :value @value-atom
    :on-change #(reset! value-atom (.. % -target -value))}])

(defn main [{:keys [fields on-submit]}]
  (let [field-atoms (mapv (fn [f] [f (r/atom "")]) fields)]
    (fn []
      [:> Box {:class "p-3"}
       [:> Heading {:as "h3" :size "4" :class "mb-2"}
        "Required information"]
       [:> Text {:as "p" :size "2" :mb "7" :class "text-gray-11"}
        "Fill out following information to proceed with your command request."]

       [:form {:on-submit (fn [e]
                            (.preventDefault e)
                            (on-submit (into {} (map (fn [[k v]] [k @v]) field-atoms))))}

        [:> Flex {:direction "column" :gap "4"}
         (doall
          (for [[field value-atom] field-atoms]
            ^{:key field}
            [field-input {:label field :value-atom value-atom}]))]

        [:> Flex {:justify "end" :gap "3" :mt "6"}
         [:> Button {:variant "soft"
                     :color "gray"
                     :type "button"
                     :on-click #(rf/dispatch [:modal->close])}
          "Cancel"]
         [:> Button {:type "submit"}
          "Send and Run"]]]])))
