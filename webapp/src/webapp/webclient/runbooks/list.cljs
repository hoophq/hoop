(ns webapp.webclient.runbooks.list
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            [re-frame.core :as rf]
            [webapp.components.button :as button]
            [webapp.components.searchbox :as searchbox]))

;; FEATURE - Runbooks separeted by directories
;; (defn is-directory? [item]
;;   (let [parts (cs/split (:name item) #"/")]
;;     (> (count parts) 1)))

;; (defn compare-paths [a b]
;;   (let [parts-a (cs/split (:name a) #"/")
;;         parts-b (cs/split (:name b) #"/")
;;         depth-a (count parts-a)
;;         depth-b (count parts-b)
;;         dir-a (is-directory? a)
;;         dir-b (is-directory? b)]
;;     (cond
;;       (and dir-a (not dir-b)) -1
;;       (and (not dir-a) dir-b) 1
;;       (not= depth-a depth-b) (compare depth-a depth-b)
;;       :else (compare parts-a parts-b))))

;; (defn sort-items [items]
;;   (sort compare-paths items))

;; (defn insert-into-tree [tree parts item]
;;   (let [part (first parts)
;;         rest-parts (rest parts)]
;;     (if (empty? rest-parts)
;;       (assoc tree part {:filename (:name item) :label (last parts)})
;;       (update tree part (fnil (fn [v] (insert-into-tree v rest-parts item)) {})))))

;; (defn transform-payload [payload]
;;   (println "PAYLOAD" payload)
;;   (println (sort-items payload))
;;   (let [items (sort-items payload)]
;;     (reduce
;;      (fn [acc item]
;;        (let [parts (cs/split (:name item) #"/")]
;;          (insert-into-tree acc parts item)))
;;      {}
;;      items)))


;; (defn random-name []
;;   (let [numberDictionary (.generate ung/NumberDictionary #js{:length 4})
;;         characterName (ung/uniqueNamesGenerator #js{:dictionaries #js[ung/animals ung/starWars]
;;                                                     :style "lowerCase"
;;                                                     :length 1})]
;;     (str characterName "-" numberDictionary)))

;; ;; Componente para representar um arquivo
;; (defn file [filename label]
;;   [:div {:class "pl-6 cursor-pointer hover:text-blue-500 text-xs text-white"
;;          :on-click #(rf/dispatch [:runbooks-plugin->set-active-runbook filename])} label])

;; ;; Componente para representar um diretório
;; (defn directory [_ _ _ filter-template-selected]
;;   (let [dropdown-status (r/atom {})]
;;     (fn [name items level]
;;       [:div {:class (str "text-xs text-white "
;;                          (when level
;;                            (str "pl-" (* level 2))))}
;;        [:div {:class "flex items-center gap-small"}
;;         (if (= (get @dropdown-status name) :open)
;;           [:> hero-solid-icon/FolderOpenIcon {:class "h-3 w-3 shrink-0 text-white"
;;                                               :aria-hidden "true"}]
;;           [:> hero-solid-icon/FolderIcon {:class "h-3 w-3 shrink-0 text-white"
;;                                           :aria-hidden "true"}])
;;         [:span {:class (str "hover:text-blue-500 hover:underline cursor-pointer "
;;                             "flex items-center")
;;                 :on-click #(swap! dropdown-status
;;                                   assoc-in [name]
;;                                   (if (= (get @dropdown-status name) :open) :closed :open))}
;;          [:span name]
;;          (if (= (get @dropdown-status name) :open)
;;            [:> hero-solid-icon/ChevronUpIcon {:class "h-4 w-4 shrink-0 text-white"
;;                                               :aria-hidden "true"}]
;;            [:> hero-solid-icon/ChevronDownIcon {:class "h-4 w-4 shrink-0 text-white"
;;                                                 :aria-hidden "true"}])]]
;;        [:div {:class (when (not= (get @dropdown-status name) :open)
;;                        "h-0 overflow-hidden")}
;;         (for [[key value] items]
;;           (if (:filename value)
;;             ^{:key (str (random-name) "file")}
;;             [file (:filename value) (:label value) filter-template-selected]
;;             ^{:key (str (random-name) "directory")}
;;             [directory key value (+ level 1)]))]])))

;; ;; Componente principal
;; (defn directory-tree [tree filter-template-selected]
;;   [:div
;;    (for [[name items] tree]
;;      ^{:key (random-name)}
;;      [directory name items 0 filter-template-selected])])
;; FEATURE - Runbooks separeted by directories

(defn- loading-list-view []
  [:div
   {:class "flex gap-small items-center py-regular text-xs text-white"}
   [:span {:class "italic"}
    "Loading runbooks"]
   [:figure {:class "w-3 flex-shrink-0 animate-spin opacity-60"}
    [:img {:src "/icons/icon-loader-circle-white.svg"}]]])

(defn- empty-templates-view []
  [:div {:class "pt-large"}
   [:div {:class "px-large text-center"}
    [:div {:class "text-white text-sm font-bold mb-small"}
     "No runbooks available in your repository!"]
    [:div {:class "text-white text-xs"}
     (str "Trouble creating a runbook file? ")
     [:a {:href "https://hoop.dev/docs/plugins/runbooks/configuring"
          :target "_blank"
          :class "underline text-blue-500"}
      "Get to know how to use our runbooks plugin."]]]])

(defn- no-integration-templates-view []
  [:div {:class "pt-large"}
   [:div {:class "flex flex-col items-center text-center"}
    [:div {:class "text-white text-sm font-bold"}
     "No Git repository connected."]
    [:div {:class "text-white text-xs mb-large"}
     "It's time to stop rewriting everything again!"]
    [button/primary
     {:text "Configure your git repository"
      :outlined true
      :on-click #(rf/dispatch [:navigate :manage-plugin {} :plugin-name "runbooks"])}]]])

(defn main []
  (let [templates (rf/subscribe [:runbooks-plugin->runbooks])
        filtered-templates (rf/subscribe [:runbooks-plugin->filtered-runbooks])
        selected-template (rf/subscribe [:runbooks-plugin->selected-runbooks])]
    (rf/dispatch [:audit->clear-session])
    (rf/dispatch [:runbooks-plugin->get-runbooks])
    (fn []
      (let [filter-template-selected (fn [template]
                                       (first (filter #(= (:name %) template) (:data @templates))))
            ;; transformed-payload (transform-payload @filtered-templates)
            ]
        (cond
          (= :loading (:status @templates)) [loading-list-view]
          (= :error (:status @templates)) [no-integration-templates-view]
          (and (empty? (:data @templates)) (= :ready (:status @templates))) [empty-templates-view]
          :else [:<>
                 [:div {:class "my-regular mr-regular"}
                  [searchbox/main
                   {:options (map #(into {} {:name (:name %)}) (:data @templates))
                    :on-change-results-cb #(rf/dispatch [:runbooks-plugin->set-filtered-runbooks %])
                    :display-key :name
                    :searchable-keys [:name]
                    :hide-results-list true
                    :placeholder "Search"
                    :loading? (= (:status @templates) :loading)
                    :name "runbooks-editor-search"
                    :dark true
                    :clear? true
                    :selected (-> @selected-template :template :name)}]]

                ;;  [directory-tree transformed-payload filter-template-selected]

                 (doall
                  (for [template @filtered-templates]
                    (let [selected? (= (:name template) (-> @selected-template :data :name))]
                      ^{:key (:name template)}
                      [:a {:href "#"
                           :on-click #(rf/dispatch [:runbooks-plugin->set-active-runbook
                                                    (filter-template-selected (:name template))])}
                       [:div {:class (str "flex gap-x-small items-center text-white "
                                          "cursor-pointer py-small transition "
                                          "text-xs hover:text-blue-500"
                                          (when selected?
                                            " text-blue-500"))}
                        [:> hero-outline-icon/DocumentIcon
                         {:class "h-3 w-3 text-white" :aria-hidden "true"}]
                        [:span
                         (:name template)]
                        (when (= (:name template) (-> @selected-template :data :name))
                          [:figure {:class "w-6 flex-shrink-0"}
                           [:img {:src "/icons/icon-check-blue.svg"}]])]])))])))))
