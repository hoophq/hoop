(ns webapp.plugins.views.plugin-configurations.container
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.headings :as h]
            [webapp.components.icon :as icon]
            [webapp.components.toggle :as toggle]
            [webapp.connections.views.connections-filter :as connections-filter]))

(defn- container-configuration-modal [& children]
  [:main
   [:header
    [h/h2 "Configurations"]]
   (into [:<>] children)])

(defn- connection-item [{:keys [connection plugin]} configuration-component]
  (let [toggle-enabled? (r/atom (some #(= (:id connection) (:id %))
                                      (:connections plugin)))]
    (fn [{:keys [connection plugin user-groups status]}]
      [:li {:class (str "flex flex-col gap-regular lg:gap-0 lg:flex-row lg:justify-between lg:items-center text-xs"
                        " text-gray-800 py-small lg:my-small lg:h-24 last:pb-0 first:pt-0"
                        " border-b border-gray-200 last:border-0")}
       [:div {:class "flex gap-small items-center"}
        [icon/regular {:icon-name "cable"
                       :size 4}]
        [:span {:class "font-bold flex-grow"}
         (:name connection)]
        [:span {:class (str "ml-2" (when (= status :loading)
                                     " pointer-events-none opacity-50"))}
         [toggle/main {:enabled? (some #(= (:id connection) (:id %))
                                       (:connections plugin))
                       :on-click (fn []
                                   (rf/dispatch [:plugins->update-plugin-connections
                                                 {:action (if @toggle-enabled?
                                                            :remove :add)
                                                  :plugin plugin
                                                  :connection connection}])
                                   (reset! toggle-enabled?
                                           (not @toggle-enabled?)))}]]]
       (when-not (nil? configuration-component)
         [:div {:class "self-end lg:self-auto"}
          [button/secondary {:text "Configure"
                             :outlined true
                             :disabled (not @toggle-enabled?)
                             :on-click #(rf/dispatch [:open-modal [container-configuration-modal
                                                                   [configuration-component {:connection connection
                                                                                             :user-groups user-groups
                                                                                             :plugin plugin}]]])}]])])))

(defn- empty-connections-list []
  [:span {:class "text-xs text-gray-600 italic"}
   "You don't have any connections :("])

(defn main [configuration-modal]
  (let [connections (rf/subscribe [:connections])
        plugin-details (rf/subscribe [:plugins->plugin-details])
        searched-connections (r/atom nil)
        user-groups (rf/subscribe [:user-groups])]
    (rf/dispatch [:connections->get-connections {:force-refresh? true}])
    (rf/dispatch [:users->get-user-groups])
    (fn []
      (let [results (if (empty? @searched-connections)
                      (:results @connections)
                      @searched-connections)]
        [:section {:id "plugin-connection-configurations"
                   :class "flex flex-col gap-small"}
         [connections-filter/main searched-connections]
         [:ul {:class "flex flex-col"}
          (when (and (not (:loading @connections))
                     (empty? results))
            [empty-connections-list])
          (doall
           (for [connection results]
             ^{:key (:name connection)}
             [connection-item {:connection connection
                               :status (:status @plugin-details)
                               :user-groups @user-groups
                               :plugin (:plugin @plugin-details)} configuration-modal]))]]))))


