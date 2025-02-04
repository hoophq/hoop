(ns webapp.connections.views.setup.type-selector
  (:require
   ["@radix-ui/themes" :refer [Avatar Box Flex Heading Text]]
   ["lucide-react" :refer [Database Network Server]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.connections.views.setup.headers :as headers]
   [webapp.connections.views.setup.state :as state]))

(def connection-type-avatars
  {"database" {:icon Database
               :color "green"}
   "server" {:icon Server
             :color "amber"}
   "network" {:icon Network
              :color "sky"}})

(defn connection-type-card [{:keys [id title description icon on-click selected?]}]
  (let [avatar (get connection-type-avatars icon)]
    [:> Box {:class (str "p-4 border rounded-lg mb-3 cursor-pointer transition-all hover:border-blue-500 "
                         (if selected?
                           "border-blue-500 bg-blue-50"
                           "border-gray-200"))
             :on-click #(on-click id)}
     [:> Flex {:gap "3" :align "center"}
      [:> Avatar {:size "4"
                  :color (:color avatar)
                  :variant "soft"
                  :fallback (r/as-element [:> (:icon avatar) {:size 18}])}]
      [:> Flex {:direction "column"}
       [:> Heading {:size "3" :weight "semibold" :class "text-[--gray-12]"} title]
       [:> Text {:size "2" :class "text-[--gray-11]"} description]]]]))

(defn main []
  (rf/dispatch [:connection-setup/initialize-state nil])

  (fn []
    (let [selected-type @(rf/subscribe [:connection-setup/connection-type])]
      [:> Box {:class "max-w-[600px] mx-auto p-6 space-y-7"}
       [headers/setup-header]

       [:> Box
        [:> Text {:as "p" :size "2" :mb "5" :class "text-[--gray-11]"}
         "Choose the resource of your new connection"]

        (for [{:keys [id] :as type} state/connection-types]
          ^{:key id}
          [connection-type-card
           (assoc type
                  :selected? (= selected-type id)
                  :on-click #(rf/dispatch [:connection-setup/select-type %]))])]])))
