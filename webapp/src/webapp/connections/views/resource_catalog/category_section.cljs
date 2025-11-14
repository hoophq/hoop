(ns webapp.connections.views.resource-catalog.category-section
  (:require
   ["@radix-ui/themes" :refer [Box Card Flex Avatar Heading Badge Text]]
   [reagent.core :as r]
   [webapp.connections.views.resource-catalog.helpers :as helpers]))


(defn connection-icon [icon-name connection-id]
  (let [image-failed? (r/atom false)]
    (fn []
      (if @image-failed?
        [:> Avatar {:size "1"
                    :variant "solid"
                    :fallback (first (str connection-id))}]
        ;; Try to load image
        [:img {:src (str "/icons/connections/" (or icon-name connection-id) "-default.svg")
               :alt connection-id
               :class "w-6 h-6"
               :on-error (fn [_]
                           (reset! image-failed? true))}]))))

(defn connection-card [connection on-click]
  (let [{:keys [id name icon-name]} connection
        badge (helpers/get-connection-badge connection)]
    [:> Box {:height "110px" :width "176px"}
     [:> Card {:size "2"
               :class "h-full w-full cursor-pointer"
               :on-click #(on-click connection)}
      [:> Flex {:direction "column" :justify "between" :gap "2" :class "h-full w-full"}
       [:> Flex {:align "center" :justify "between" :gap "2"}
        [:> Box
         [connection-icon icon-name id]]

        (when (seq badge)
          (for [badge badge]
            ^{:key (:text badge)}
            [:> Badge {:color (:color badge)
                       :variant "solid"
                       :size "1"}
             (:text badge)]))]

       [:> Text {:size "2" :weight "medium" :align "left" :class "text-[--gray-12]"}
        name]]]]))

(defn main [title connections on-connection-click]
  (when (seq connections)
    [:> Box {:class "space-y-radix-5"}
     [:> Heading {:as "h3" :size "5" :weight "bold" :class "mb-6 text-[--gray-12]"}
      title]
     [:> Flex {:direction "row" :wrap "wrap" :gap "4"}
      (for [connection connections]
        ^{:key (:id connection)}
        [connection-card connection (or (:action connection) on-connection-click)])]]))
