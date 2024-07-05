(ns webapp.plugins.views.plugin-configurations.runbooks
  (:require [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.forms :as forms]
            [webapp.components.headings :as h]
            [webapp.components.tabs :as tabs]
            [webapp.plugins.views.plugin-configurations.container :as plugin-configuration-container]))

(defn configuration-modal [{:keys [connection plugin]}]
  (let [current-connection-config (first (filter #(= (:id connection)
                                                     (:id %))
                                                 (:connections plugin)))
        repository-path-value (r/atom (or (first (:config current-connection-config)) ""))]
    (fn [{:keys [plugin]}]
      [:section
       {:class "flex flex-col px-small pt-regular"}
       [:form
        {:on-submit (fn [e]
                      (.preventDefault e)
                      (let [connection  (merge current-connection-config
                                               {:config [@repository-path-value]})
                            dissoced-connections (filter #(not= (:id %)
                                                                (:id connection))
                                                         (:connections plugin))
                            new-plugin-data (assoc plugin :connections (conj
                                                                        dissoced-connections
                                                                        connection))]
                        (rf/dispatch [:plugins->update-plugin new-plugin-data])))}
        [:header
         [:div {:class "font-bold text-xs"}
          "Scripts path"]
         [:footer
          {:class "text-xs text-gray-600 pb-1"}
          (str "Provide a path for this connection be able to access in the git repository. "
               "No path added is the same of root path.")]]
        [forms/input {:value @repository-path-value
                      :id "repository-path-value"
                      :name "repository-path-value"
                      :on-change #(reset! repository-path-value (-> % .-target .-value))
                      :placeholder "dev/queries"}]
        [:footer
         {:class "flex justify-end"}
         [:div {:class "flex-shrink"}
          [button/primary {:text "Save"
                           :variant :small
                           :type "submit"}]]]]])))

(defn- configurations-view [plugin-details]
  (let [envvars (-> @plugin-details :plugin :config :envvars)
        edit? (r/atom (not (nil? (-> @plugin-details :plugin :config))))
        git-url (r/atom (or (:GIT_URL envvars) ""))
        git-ssh-key (r/atom (or (:GIT_SSH_KEY envvars) ""))]
    (fn []
      [:section
       [:div {:class "grid grid-cols-3 gap-large my-large"}
        [:div {:class "col-span-1"}
         [h/h3 "Git repository" {:class "text-gray-800"}]
         [:span {:class "block text-sm mb-regular text-gray-600"}
          "Here you will integrate with one repository to consume your runbooks"]]
        [:div {:class "col-span-2"}
         [:form
          {:class "mb-regular"
           :on-submit (fn [e]
                        (.preventDefault e)
                        (rf/dispatch [:runbooks-plugin->git-config {:git-url @git-url
                                                                    :git-ssh-key @git-ssh-key}])
                        (reset! edit? true))}
          [:div {:class "grid gap-regular"}
           [forms/input {:label "Git URL"
                         :on-change #(reset! git-url (-> % .-target .-value))
                         :classes "whitespace-pre overflow-x"
                         :placeholder "git@github.com:company/repository.git"
                         :disabled @edit?
                         :type "password"
                         :value @git-url}]
           [forms/textarea {:label "Deploy ssh key"
                            :on-change #(reset! git-ssh-key (-> % .-target .-value))
                            :classes "whitespace-pre overflow-x"
                            :placeholder "Deploy ssh key"
                            :disabled @edit?
                            :value @git-ssh-key}]]
          [:div {:class "grid grid-cols-3 justify-items-end"}
           (if @edit?
             [:div {:class "col-end-4 w-full"}
              (button/primary {:text "Edit"
                               :type "button"
                               :on-click (fn []
                                           (reset! edit? false)
                                           (reset! git-url "")
                                           (reset! git-ssh-key ""))
                               :full-width true})]

             [:div {:class "col-end-4 w-full"}
              (button/primary {:text "Save"
                               :type "submit"
                               :full-width true})])]]]]])))

(defmulti ^:private selected-view identity)
(defmethod ^:private selected-view :Connections [_ _]
  [plugin-configuration-container/main configuration-modal])

(defmethod ^:private selected-view :Configurations [_ plugin-details]
  [configurations-view plugin-details])

(defn main []
  (let [plugin-details (rf/subscribe [:plugins->plugin-details])
        selected-tab (r/atom :Connections)]
    (fn []
      (let [tabs [:Connections :Configurations]]
        [:div
         [tabs/tabs {:on-change #(reset! selected-tab %)
                     :tabs tabs}]
         [selected-view @selected-tab plugin-details]]))))

