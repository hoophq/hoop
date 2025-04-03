(ns webapp.reviews.radix-filter
  (:require ["@radix-ui/themes" :refer [Select]]
            [reagent.core :as r]
            [clojure.string :as cs]))

(defn check-icon []
  [:svg
   {:xmlns "http://www.w3.org/2000/svg"
    :viewBox "0 0 20 20"
    :fill "currentColor"
    :class "w-4 h-4"}
   [:path
    {:fill-rule "evenodd"
     :d "M16.704 4.153a.75.75 0 01.143 1.052l-8 10.5a.75.75 0 01-1.127.075l-4.5-4.5a.75.75 0 011.06-1.06l3.894 3.893 7.48-9.817a.75.75 0 011.05-.143z"
     :clip-rule "evenodd"}]])

(defn chevron-down-icon []
  [:svg
   {:xmlns "http://www.w3.org/2000/svg"
    :viewBox "0 0 20 20"
    :fill "currentColor"
    :class "w-5 h-5"}
   [:path
    {:fill-rule "evenodd"
     :d "M5.23 7.21a.75.75 0 011.06.02L10 11.168l3.71-3.938a.75.75 0 111.08 1.04l-4.25 4.5a.75.75 0 01-1.08 0l-4.25-4.5a.75.75 0 01.02-1.06z"
     :clip-rule "evenodd"}]])

(defn- option
  [item _]
  ^{:key (:value item)}
  [:> Select.Item {:value (:value item)} (:text item)])

(defn status-filter [{:keys [options selected on-change]}]
  [:div {:class "text-sm w-full mb-regular"}
   [:div {:class "flex items-center gap-2 mb-1"}
    [:label {:class "block text-xs font-semibold text-[--gray-12]"} "Status"]]
   [:> Select.Root {:size "2"
                    :value selected
                    :onValueChange on-change}
    [:> Select.Trigger {:placeholder "Select status"
                        :variant "surface"
                        :color "indigo"
                        :class "w-full"}]
    [:> Select.Content {:position "popper"
                        :color "indigo"}
     [:> Select.Item {:value ""} "All"]
     (map #(option % selected) options)]]])
