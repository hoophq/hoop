(ns webapp.runbooks.views.panel
  (:require [re-frame.core :as rf]
            [webapp.components.button :as button]
            [webapp.components.headings :as h]
            [webapp.components.loaders :as loaders]
            [webapp.components.searchbox :as searchbox]
            [webapp.events.connections]
            [webapp.runbooks.views.template-view :as template-view]
            [webapp.config :as config]))

(defn- loading-list-view []
  [:div {:class "flex items-center justify-center h-full"}
   [loaders/simple-loader]])

(defn- empty-templates-view []
  [:div {:class "pt-large"}
   [:figure
    {:class "w-3/4 mx-auto p-regular"}
    [:img {:src (str config/webapp-url "/images/illustrations/disk.svg")
           :class "w-full"}]]
   [:div {:class "px-large text-center"}
    [:div {:class "text-gray-700 text-sm font-bold mb-small"}
     "No runbooks available in your repository!"]
    [:div {:class "text-gray-500 text-xs"}
     (str "Trouble creating a runbook file? ")
     [:a {:href "https://hoop.dev/docs/learn/runbooks/configuration"
          :target "_blank"
          :class "underline text-blue-500"}
      "Get to know how to use our runbooks plugin."]]]])

(defn- no-integration-templates-view []
  [:div {:class "pt-large flex flex-col gap-regular items-center"}
   [:figure {:class "w-3/4"}
    [:img {:src (str config/webapp-url "/images/illustrations/typingmachine.svg")
           :class "w-full"}]]
   [:div {:class "flex flex-col items-center text-center"}
    [:div {:class "text-gray-700 text-sm font-bold"}
     "No Git repository connected."]
    [:div {:class "text-gray-500 text-xs mb-large"}
     "It's time to stop rewriting everything again!"]
    [button/secondary
     {:text "Configure your git repository"
      :outlined true
      :on-click (fn []
                  (rf/dispatch [:navigate :manage-plugin {} :plugin-name "runbooks"]))}]]])

(defn- templates-list [_ _]
  (let [filtered-templates (rf/subscribe [:runbooks-plugin->filtered-runbooks])
        selected-template (rf/subscribe [:runbooks-plugin->selected-runbooks])]
    (fn [templates]
      [:<>
       (cond
         (= :loading (:status templates)) [loading-list-view]
         (= :error (:status templates)) [no-integration-templates-view]
         :else [:div {:class "overflow-auto"}
                [:header {:class "flex items-center pr-regular"}
                 [h/h4 "Your Runbooks" {:class "flex-grow"}]]

                [:div {:class "my-regular mr-regular"}
                 [searchbox/main {:options (map #(into {} {:name (:name %)}) (:data templates))
                                  :on-change-results-cb #(rf/dispatch [:runbooks-plugin->set-filtered-runbooks %])
                                  :display-key :name
                                  :searchable-keys [:name]
                                  :hide-results-list true
                                  :placeholder "Type to search your Runbook"
                                  :loading? (= (:status templates) :loading)
                                  :name "templates-search"
                                  :clear? true
                                  :list-classes "min-w-96"
                                  :selected (-> @selected-template :template :name)}]]
                (when (empty? (:data templates)) [empty-templates-view])
                (doall
                 (for [template @filtered-templates]
                   (let [selected? (= (:name template) (-> @selected-template :data :name))
                         filter-template-selected (fn [template]
                                                    (first (filter #(= (:name %) template) (:data templates))))]
                     ^{:key (:name template)}
                     [:a {:href (str "/runbooks?selected=" (:name template))
                          :on-click #(rf/dispatch [:runbooks-plugin->set-active-runbook
                                                   (filter-template-selected (:name template))])}
                      [:div
                       {:class (str "flex gap-x-small items-center "
                                    "cursor-pointer py-small transition "
                                    "text-xs text-gray-700 hover:text-blue-500"
                                    (when selected?
                                      " text-blue-500"))}
                       [:figure {:class "w-3 flex-shrink-0"}
                        [:img {:src (if selected?
                                      (str config/webapp-url "/icons/icon-document-blue.svg")
                                      (str config/webapp-url "/icons/icon-document-black.svg"))}]]
                       [:span
                        (:name template)]
                       (when (= (:name template) (-> @selected-template :data :name))
                         [:figure {:class "w-6 flex-shrink-0"}
                          [:img {:src (str config/webapp-url "/icons/icon-check-blue.svg")}]])]])))])])))

(defn panel [_]
  (let [templates (rf/subscribe [:runbooks-plugin->runbooks])
        selected-template (rf/subscribe [:runbooks-plugin->selected-runbooks])
        primary-connection (rf/subscribe [:connections/selected])
        selected-connections (rf/subscribe [:connection-selection/selected])]
    (rf/dispatch [:audit->clear-session])
    (rf/dispatch [:runbooks-plugin->get-runbooks
                  (map :name (concat
                              (when @primary-connection [@primary-connection])
                              @selected-connections))])
    (fn [connection]
      (let [search (.. js/window -location -search)
            url-search-params (new js/URLSearchParams search)
            url-params-list (js->clj (for [q url-search-params] q))
            url-params-map (into (sorted-map) url-params-list)
            filter-template-selected (fn [template]
                                       (first
                                        (filter #(= (:name %) template)
                                                (:data @templates))))]
        ;; when selected url param exists, set it as active runbook
        (when (and (get url-params-map "selected")
                   (not= (-> @selected-template :data :name)
                         (get url-params-map "selected")))

          (rf/dispatch [:runbooks-plugin->set-active-runbook
                        (filter-template-selected (get url-params-map "selected"))]))
        [:div {:class "h-full grid grid-cols-1 lg:grid-cols-8 gap-regular"}
         [:div {:class "templates-list bg-white p-regular rounded-lg lg:col-span-3 h-full"}
          [templates-list @templates (:name connection)]]
         [:div {:class "templates-body bg-white p-regular rounded-lg lg:col-span-5 p-regular"}
          [template-view/main {:runbook @selected-template
                               :preselected-connection (or (:name connection) (get url-params-map "connection"))}]]]))))
