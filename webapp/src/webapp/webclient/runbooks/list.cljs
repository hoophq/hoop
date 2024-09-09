(ns webapp.webclient.runbooks.list
  (:require ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["lucide-react" :refer [FolderClosed FolderOpen File]]
            ["@radix-ui/themes" :refer [Button]]
            [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]))

(defn sort-tree [data]
  (let [non-empty-keys (->> data
                            (filter (fn [[k v]] (seq v)))
                            (sort-by first))
        empty-keys (->> data
                        (filter (fn [[k v]] (empty? v)))
                        (sort-by first))]
    (into {} (concat non-empty-keys empty-keys))))

(defn split-path [path]
  (let [[folder & rest] (cs/split path #"/")]
    [folder (cs/join "/" rest)]))

(defn insert-into-tree [tree [folder filename]]
  (if (empty? filename)
    (update tree folder (fnil conj []))
    (update tree folder (fnil conj []) (str folder "/" filename))))

(defn transform-payload [payload]
  (reduce
   (fn [tree {:keys [name]}]
     (let [[folder filename] (split-path name)]
       (insert-into-tree tree [folder filename])))
   {}
   payload))

(defn file [filename filter-template-selected]
  [:div {:class "flex items-center gap-2 pl-6 pb-4 hover:underline cursor-pointer text-xs text-white whitespace-pre"
         :on-click #(rf/dispatch [:runbooks-plugin->set-active-runbook
                                  (filter-template-selected filename)])}
   [:div
    [:> File {:size 14
              :color "white"}]]
   [:span {:class "block truncate"}
    filename]])

(defn directory [_ _ _ filter-template-selected]
  (let [dropdown-status (r/atom {})]
    (fn [name items level]
      (if (empty? items)
        [:div {:class "flex items-center gap-2 pb-4 hover:underline cursor-pointer text-xs text-white whitespace-pre"
               :on-click #(rf/dispatch [:runbooks-plugin->set-active-runbook
                                        (filter-template-selected name)])}
         [:div
          [:> File {:size 14
                    :color "white"}]]
         [:span {:class "block truncate"}
          name]]

        [:div {:class (str "text-xs text-white "
                           (when level
                             (str "pl-" (* level 2))))}
         [:div {:class "flex pb-4 items-center gap-small"}
          (if (= (get @dropdown-status name) :open)
            [:div
             [:> FolderOpen {:size 14
                             :color "white"}]]
            [:div
             [:> FolderClosed {:size 14
                               :color "white"}]])
          [:span {:class (str "hover:underline cursor-pointer "
                              "flex items-center")
                  :on-click #(swap! dropdown-status
                                    assoc-in [name]
                                    (if (= (get @dropdown-status name) :open) :closed :open))}
           [:span name]
           (if (= (get @dropdown-status name) :open)
             [:> hero-solid-icon/ChevronUpIcon {:class "h-4 w-4 shrink-0 text-white"
                                                :aria-hidden "true"}]
             [:> hero-solid-icon/ChevronDownIcon {:class "h-4 w-4 shrink-0 text-white"
                                                  :aria-hidden "true"}])]]
         [:div {:class (when (not= (get @dropdown-status name) :open)
                         "h-0 overflow-hidden")}
          (for [item items]
            ^{:key item}
            [file item filter-template-selected])]]))))

(defn directory-tree [tree filter-template-selected]
  [:div
   (for [[name items] tree]
     ^{:key name}
     [directory name items 0 filter-template-selected])])

(defn- loading-list-view []
  [:div {:class "flex gap-small items-center py-regular text-xs text-white"}
   [:span {:class "italic"}
    "Loading runbooks"]
   [:figure {:class "w-3 flex-shrink-0 animate-spin opacity-60"}
    [:img {:src "/icons/icon-loader-circle-white.svg"}]]])

(defn- empty-templates-view []
  [:div {:class "text-center"}
   [:div {:class "text-gray-400 text-xs"}
    "There are no Runbooks available for this connection."]])

(defn- no-integration-templates-view []
  [:div {:class "pt-large"}
   [:div {:class "flex flex-col items-center text-center"}
    [:div {:class "text-gray-400 text-xs mb-large"}
     "Configure your Git repository to enable your Runbooks."]
    [:> Button {:color "indigo"
                :size "3"
                :variant "ghost"
                :class-name "dark"
                :radius "medium"
                :on-click #(rf/dispatch [:navigate :manage-plugin {:tab "configurations"} :plugin-name "runbooks"])}
     "Go to Configurations"]]])

(defn main []
  (fn [templates filtered-templates]
    (let [filter-template-selected (fn [template]
                                     (first (filter #(= (:name %) template) (:data @templates))))
          transformed-payload (sort-tree (transform-payload @filtered-templates))]
      (cond
        (= :loading (:status @templates)) [loading-list-view]
        (= :error (:status @templates)) [no-integration-templates-view]
        (and (empty? (:data @templates)) (= :ready (:status @templates))) [empty-templates-view]
        (empty? @filtered-templates) [:div {:class "text-center text-xs text-gray-400 font-normal"}
                                      "There's no runbook matching your search."]
        :else [directory-tree transformed-payload filter-template-selected]))))
